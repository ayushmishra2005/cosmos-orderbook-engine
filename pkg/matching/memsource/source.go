package memsource

import (
	"bytes"
	"errors"
	"sort"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

var (
	ErrClosed    = errors.New("memsource: closed")
	ErrExhausted = errors.New("memsource: exhausted")
	ErrSide      = errors.New("memsource: order side does not match book")
	ErrMarket    = errors.New("memsource: mixed markets")
	ErrDuplicate = errors.New("memsource: duplicate book key")
)

// Source is a forward cursor. Duplicate book keys are rejected so the sorted
// order is the only order consistent with byte comparison of the book key.
type Source struct {
	orders []domain.Order
	index  int
	closed bool
}

// NewAskSource sorts sell orders as an ask iterator: lowest price, then oldest sequence.
func NewAskSource(orders []domain.Order) (*Source, error) {
	return newSource(domain.SideSell, orders)
}

// NewBidSource sorts buy orders as a bid iterator: highest price, then oldest sequence.
func NewBidSource(orders []domain.Order) (*Source, error) {
	return newSource(domain.SideBuy, orders)
}

func newSource(side domain.Side, orders []domain.Order) (*Source, error) {
	if len(orders) == 0 {
		return &Source{}, nil
	}

	type positioned struct {
		order domain.Order
		key   []byte
	}
	items := make([]positioned, len(orders))
	var market domain.MarketID
	for i, order := range orders {
		if err := order.ValidateResting(); err != nil {
			return nil, err
		}
		if order.Side != side {
			return nil, ErrSide
		}
		if i == 0 {
			market = order.MarketID
		} else if order.MarketID != market {
			return nil, ErrMarket
		}
		var (
			key []byte
			err error
		)
		if side == domain.SideSell {
			key, err = canonical.EncodeAskKey(order.MarketID, order.Price, order.Sequence)
		} else {
			key, err = canonical.EncodeBidKey(order.MarketID, order.Price, order.Sequence)
		}
		if err != nil {
			return nil, err
		}
		items[i] = positioned{order: cloneOrder(order), key: key}
	}

	sort.Slice(items, func(i, j int) bool {
		return bytes.Compare(items[i].key, items[j].key) < 0
	})
	out := make([]domain.Order, len(items))
	for i := range items {
		if i > 0 && bytes.Equal(items[i-1].key, items[i].key) {
			return nil, ErrDuplicate
		}
		out[i] = items[i].order
	}
	return &Source{orders: out}, nil
}

// Reset rewinds the cursor. Benchmarks use it so setup stays outside the timer.
func (s *Source) Reset() {
	s.index = 0
	s.closed = false
}

// Peek returns a copy of the order at the cursor.
func (s *Source) Peek() (domain.Order, bool, error) {
	if s.closed {
		return domain.Order{}, false, ErrClosed
	}
	if s.index >= len(s.orders) {
		return domain.Order{}, false, nil
	}
	return cloneOrder(s.orders[s.index]), true, nil
}

// Next advances one order.
func (s *Source) Next() error {
	if s.closed {
		return ErrClosed
	}
	if s.index >= len(s.orders) {
		return ErrExhausted
	}
	s.index++
	return nil
}

// Close is idempotent.
func (s *Source) Close() error {
	s.closed = true
	return nil
}

func cloneOrder(o domain.Order) domain.Order {
	if len(o.Owner) > 0 {
		o.Owner = append([]byte(nil), o.Owner...)
	}
	return o
}
