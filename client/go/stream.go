package orderbook

import (
	"context"
	"errors"
	"sync"
	"time"
)

type streamFilter struct {
	marketID uint64
	kinds    map[Kind]struct{}
}

func (f streamFilter) match(ev Event) bool {
	if _, ok := f.kinds[ev.Kind]; !ok {
		return false
	}
	if f.marketID == 0 {
		return true
	}
	market, ok := eventMarket(ev)
	return ok && market == f.marketID
}

// Subscription receives committed events until the channel closes.
// Err is set before the channel closes.
type Subscription struct {
	Events <-chan Event

	mu  sync.Mutex
	err error
}

// Err returns the reason the event channel closed.
// Context cancellation is context.Canceled.
func (s *Subscription) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

type subscription struct {
	ch   chan Event
	pub  *Subscription
	once sync.Once
}

func newSubscription(buffer int) *subscription {
	ch := make(chan Event, buffer)
	return &subscription{ch: ch, pub: &Subscription{Events: ch}}
}

func (s *subscription) stop(err error) {
	s.once.Do(func() {
		s.pub.mu.Lock()
		s.pub.err = err
		s.pub.mu.Unlock()
		close(s.ch)
	})
}

func (s *subscription) push(ev Event) error {
	select {
	case s.ch <- ev:
		return nil
	default:
		return ErrSlowConsumer
	}
}

func (s *subscription) emit(events []Event, f streamFilter) error {
	for _, ev := range events {
		if !f.match(ev) {
			continue
		}
		if err := s.push(ev); err != nil {
			return err
		}
	}
	return nil
}

// SubscribeTrades receives committed trades for one market, in chain order.
func (c *Client) SubscribeTrades(ctx context.Context, marketID uint64) (*Subscription, error) {
	if marketID == 0 {
		return nil, fmtMarket()
	}
	return c.subscribe(ctx, streamFilter{marketID: marketID, kinds: kindSet(KindTradeExecuted)})
}

// SubscribeOrders receives committed order events for one market, in chain order.
func (c *Client) SubscribeOrders(ctx context.Context, marketID uint64) (*Subscription, error) {
	if marketID == 0 {
		return nil, fmtMarket()
	}
	return c.subscribe(ctx, streamFilter{
		marketID: marketID,
		kinds: kindSet(
			KindOrderAccepted,
			KindOrderPartiallyFilled,
			KindOrderFilled,
			KindOrderCancelled,
			KindOrderExpired,
		),
	})
}

// SubscribeBatches receives committed batch-finalized events.
func (c *Client) SubscribeBatches(ctx context.Context) (*Subscription, error) {
	return c.subscribe(ctx, streamFilter{kinds: kindSet(KindBatchFinalized)})
}

func fmtMarket() error {
	return errInvalid("market id")
}

func errInvalid(what string) error {
	return errors.Join(ErrInvalidArgument, errors.New(what))
}

func kindSet(kinds ...Kind) map[Kind]struct{} {
	out := make(map[Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		out[kind] = struct{}{}
	}
	return out
}

func (c *Client) subscribe(ctx context.Context, f streamFilter) (*Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.dialLive(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.Join(ErrRPCUnavailable, err)
	}
	sub := newSubscription(c.buffer)
	go c.serve(ctx, sub, conn, f)
	return sub.pub, nil
}

func (c *Client) serve(ctx context.Context, sub *subscription, conn liveConn, f streamFilter) {
	defer conn.Close()
	// Seed the replay cursor from the height at subscribe time. A disconnect
	// before any decoded event still has a block to replay from.
	var last int64
	if c.tip != nil {
		if tip, err := c.tip(ctx); err == nil && tip > 0 {
			last = tip
		}
	}
	delay := c.delay
	connected := true
	frames, faults := readFrames(ctx, conn, c.buffer)
	for {
		if ctx.Err() != nil {
			sub.stop(ctx.Err())
			return
		}
		if !connected {
			if err := sleepCtx(ctx, delay); err != nil {
				sub.stop(err)
				return
			}
			delay = growDelay(delay, c.delayMax)
			next, err := c.dialLive(ctx)
			if err != nil {
				if ctx.Err() != nil {
					sub.stop(ctx.Err())
					return
				}
				continue
			}
			conn.Close()
			conn = next
			frames, faults = readFrames(ctx, conn, c.buffer)
			connected = true
			delay = c.delay
			if last > 0 {
				if err := c.replay(ctx, sub, f, last, &last); err != nil {
					if errors.Is(err, ErrSlowConsumer) || ctx.Err() != nil {
						if ctx.Err() != nil {
							sub.stop(ctx.Err())
						} else {
							sub.stop(err)
						}
						return
					}
					conn.Close()
					connected = false
					continue
				}
			}
		}
		select {
		case <-ctx.Done():
			sub.stop(ctx.Err())
			return
		case err := <-faults:
			if ctx.Err() != nil {
				sub.stop(ctx.Err())
				return
			}
			if errors.Is(err, ErrSlowConsumer) {
				sub.stop(ErrSlowConsumer)
				return
			}
			conn.Close()
			connected = false
		case msg, ok := <-frames:
			if !ok {
				conn.Close()
				connected = false
				continue
			}
			decode := c.decode
			if decode == nil {
				decode = decodeMessage
			}
			events, err := decode(msg)
			if err != nil {
				continue
			}
			for _, ev := range events {
				if ev.ID.Height > last {
					last = ev.ID.Height
				}
			}
			if err := sub.emit(events, f); err != nil {
				sub.stop(err)
				return
			}
		}
	}
}

func (c *Client) replay(ctx context.Context, sub *subscription, f streamFilter, from int64, last *int64) error {
	tip, err := c.tip(ctx)
	if err != nil {
		return err
	}
	if tip < from {
		return nil
	}
	for height := from; height <= tip; height++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := c.pages(ctx, height)
		if err != nil {
			return err
		}
		for _, ev := range page {
			if ev.ID.Height > *last {
				*last = ev.ID.Height
			}
		}
		if err := sub.emit(page, f); err != nil {
			return err
		}
	}
	return nil
}

func readFrames(ctx context.Context, conn liveConn, buffer int) (<-chan []byte, <-chan error) {
	frames := make(chan []byte, buffer)
	faults := make(chan error, 1)
	go func() {
		for {
			msg, err := conn.Recv(ctx)
			if err != nil {
				faults <- err
				return
			}
			select {
			case frames <- msg:
			case <-ctx.Done():
				faults <- ctx.Err()
				return
			default:
				faults <- ErrSlowConsumer
				return
			}
		}
	}()
	return frames, faults
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func growDelay(current, max time.Duration) time.Duration {
	next := current * 2
	if next < current || next > max {
		return max
	}
	return next
}
