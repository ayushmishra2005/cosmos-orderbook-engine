package orderbook

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
)

func TestParseMalformedEventDoesNotPanic(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatal("parser panicked")
		}
	}()
	if _, err := decodeMessage([]byte("not-json")); err == nil {
		t.Fatal("expected decode error")
	}
	_, ok := parseEvent(3, "AA", 1, abci.Event{
		Type: "trade_executed",
		Attributes: []abci.EventAttribute{
			{Key: "market_id", Value: "1"},
			{Key: "sequence", Value: "nope"},
			{Key: "price", Value: "10"},
			{Key: "quantity", Value: "1"},
			{Key: "maker_fee", Value: "0"},
			{Key: "taker_fee", Value: "0"},
		},
	})
	if ok {
		t.Fatal("malformed trade was accepted")
	}
	events := eventsFromABCI(4, "BB", []abci.Event{
		{Type: "trade_executed", Attributes: []abci.EventAttribute{{Key: "price", Value: "x"}}},
		{
			Type: "trade_executed",
			Attributes: []abci.EventAttribute{
				{Key: "market_id", Value: "1"}, {Key: "sequence", Value: "2"},
				{Key: "price", Value: "9"}, {Key: "quantity", Value: "3"},
				{Key: "maker_fee", Value: "0"}, {Key: "taker_fee", Value: "1"},
			},
		},
	})
	if len(events) != 1 || events[0].Trade.Sequence != 2 || events[0].ID.Index != 1 {
		t.Fatalf("events %#v", events)
	}
}

func TestEventOrderAndMarketFilter(t *testing.T) {
	raw := []abci.Event{
		tradeEvent(1, 1),
		tradeEvent(2, 1),
		tradeEvent(1, 2),
	}
	parsed := eventsFromABCI(9, "CC", raw)
	if len(parsed) != 3 || parsed[0].Trade.MarketID != 1 || parsed[1].Trade.MarketID != 2 || parsed[2].Trade.Sequence != 2 {
		t.Fatalf("order %#v", parsed)
	}
	if parsed[0].ID.Index != 0 || parsed[2].ID.Index != 2 || parsed[0].ID.TxHash != "CC" {
		t.Fatalf("identity %#v", parsed)
	}
	client := testStreamClient(1, func(msg []byte) ([]Event, error) {
		if string(msg) == "bad" {
			return nil, errors.New("malformed")
		}
		return parsed, nil
	})
	conn := &scriptConn{msgs: [][]byte{[]byte("bad"), []byte("ok")}, block: make(chan struct{})}
	client.dialLive = func(context.Context) (liveConn, error) { return conn, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := client.SubscribeTrades(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	first := readEvent(t, sub)
	second := readEvent(t, sub)
	if first.Trade.Sequence != 1 || second.Trade.Sequence != 2 || first.Trade.MarketID != 1 {
		t.Fatalf("filtered %#v %#v", first, second)
	}
	if first.ID != parsed[0].ID || second.ID != parsed[2].ID {
		t.Fatal("identity changed")
	}
	cancel()
	waitClosed(t, sub)
	if !errors.Is(sub.Err(), context.Canceled) {
		t.Fatal(sub.Err())
	}
}

func TestStreamReconnectDuplicateAndCancel(t *testing.T) {
	ev := eventsFromABCI(7, "DD", []abci.Event{tradeEvent(1, 4)})[0]
	next := eventsFromABCI(8, "EE", []abci.Event{tradeEvent(1, 5)})[0]
	var dials int
	var mu sync.Mutex
	client := testStreamClient(8, func(msg []byte) ([]Event, error) {
		if string(msg) == "7" {
			return []Event{ev}, nil
		}
		return []Event{next}, nil
	})
	client.tip = func(context.Context) (int64, error) { return 7, nil }
	client.pages = func(_ context.Context, height int64) ([]Event, error) {
		if height != 7 {
			t.Fatalf("unexpected height %d", height)
		}
		return []Event{ev}, nil
	}
	client.dialLive = func(context.Context) (liveConn, error) {
		mu.Lock()
		dials++
		n := dials
		mu.Unlock()
		if n == 1 {
			return &scriptConn{msgs: [][]byte{[]byte("7")}, err: ErrStreamDisconnected}, nil
		}
		return &scriptConn{msgs: [][]byte{[]byte("8")}, block: make(chan struct{})}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := client.SubscribeTrades(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	a := readEvent(t, sub)
	b := readEvent(t, sub)
	c := readEvent(t, sub)
	if a.ID != b.ID || a.ID.Height != 7 || a.ID.TxHash != "DD" {
		t.Fatalf("duplicate identity a=%+v b=%+v", a.ID, b.ID)
	}
	if c.ID.Height != 8 || c.Trade.Sequence != 5 {
		t.Fatalf("resume %+v", c)
	}
	cancel()
	waitClosed(t, sub)
}

func TestSlowConsumerAndConnectionFailure(t *testing.T) {
	client := testStreamClient(1, func(msg []byte) ([]Event, error) {
		n, _ := strconv.Atoi(string(msg))
		return eventsFromABCI(int64(n), "FF", []abci.Event{tradeEvent(1, uint64(n))}), nil
	})
	client.dialLive = func(context.Context) (liveConn, error) {
		return nil, errors.New("refused")
	}
	if _, err := client.SubscribeTrades(context.Background(), 1); !errors.Is(err, ErrRPCUnavailable) {
		t.Fatal(err)
	}

	client.buffer = 1
	client.dialLive = func(context.Context) (liveConn, error) {
		return &scriptConn{msgs: [][]byte{[]byte("1"), []byte("2"), []byte("3")}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := client.SubscribeTrades(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, sub)
	if !errors.Is(sub.Err(), ErrSlowConsumer) {
		t.Fatal(sub.Err())
	}
}

func TestCancelDuringReconnect(t *testing.T) {
	ev := eventsFromABCI(2, "11", []abci.Event{tradeEvent(1, 1)})[0]
	client := testStreamClient(4, func([]byte) ([]Event, error) { return []Event{ev}, nil })
	client.delay = time.Hour
	client.delayMax = time.Hour
	client.dialLive = func(context.Context) (liveConn, error) {
		return &scriptConn{msgs: [][]byte{[]byte("x")}, err: ErrStreamDisconnected}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := client.SubscribeTrades(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := readEvent(t, sub)
	if got.ID != ev.ID {
		t.Fatal(got.ID)
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	waitClosed(t, sub)
	if !errors.Is(sub.Err(), context.Canceled) {
		t.Fatal(sub.Err())
	}
}

func testStreamClient(buffer int, decode func([]byte) ([]Event, error)) *Client {
	return &Client{
		buffer:   buffer,
		delay:    time.Millisecond,
		delayMax: 5 * time.Millisecond,
		decode:   decode,
		tip:      func(context.Context) (int64, error) { return 0, nil },
		pages:    func(context.Context, int64) ([]Event, error) { return nil, nil },
	}
}

func tradeEvent(market, seq uint64) abci.Event {
	return abci.Event{
		Type: "trade_executed",
		Attributes: []abci.EventAttribute{
			{Key: "market_id", Value: strconv.FormatUint(market, 10)},
			{Key: "sequence", Value: strconv.FormatUint(seq, 10)},
			{Key: "price", Value: "10"},
			{Key: "quantity", Value: "1"},
			{Key: "maker_fee", Value: "0"},
			{Key: "taker_fee", Value: "0"},
		},
	}
}

func readEvent(t *testing.T, sub *Subscription) Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case ev, ok := <-sub.Events:
		if !ok {
			t.Fatal(sub.Err())
		}
		return ev
	case <-timer.C:
		t.Fatal("timeout waiting for event")
	}
	return Event{}
}

func waitClosed(t *testing.T, sub *Subscription) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-sub.Events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("subscription stayed open")
		}
	}
}

type scriptConn struct {
	mu     sync.Mutex
	msgs   [][]byte
	i      int
	err    error
	block  chan struct{}
	closed chan struct{}
}

func (s *scriptConn) Recv(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	if s.i < len(s.msgs) {
		msg := s.msgs[s.i]
		s.i++
		s.mu.Unlock()
		return msg, nil
	}
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.block:
		return nil, ErrStreamDisconnected
	}
}

func (s *scriptConn) Close() error { return nil }
