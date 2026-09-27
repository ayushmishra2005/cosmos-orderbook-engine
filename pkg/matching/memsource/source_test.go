package memsource

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestAskSourceMatchesKeyOrder(t *testing.T) {
	t.Parallel()

	orders := []domain.Order{
		resting(3, domain.SideSell, 100, 3),
		resting(1, domain.SideSell, 90, 1),
		resting(2, domain.SideSell, 100, 2),
		resting(4, domain.SideSell, 90, 4),
	}
	src, err := NewAskSource(orders)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	var prev []byte
	wantSeq := []domain.Sequence{1, 4, 2, 3}
	for i, seq := range wantSeq {
		got, ok, err := src.Peek()
		if err != nil || !ok {
			t.Fatalf("peek %d: ok=%v %v", i, ok, err)
		}
		if got.Sequence != seq {
			t.Fatalf("peek %d sequence %d, want %d", i, got.Sequence, seq)
		}
		key, err := canonical.EncodeAskKey(got.MarketID, got.Price, got.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		if prev != nil && bytes.Compare(prev, key) >= 0 {
			t.Fatalf("key %d is not after the previous ask key", i)
		}
		prev = key
		got.Owner[0] = 'Z'
		if err := src.Next(); err != nil {
			t.Fatal(err)
		}
		again, ok, err := src.Peek()
		if i < len(wantSeq)-1 && (err != nil || !ok || again.Owner[0] == 'Z') {
			t.Fatal("peek alias or failed to advance")
		}
	}
	if _, ok, err := src.Peek(); err != nil || ok {
		t.Fatalf("exhausted peek ok=%v %v", ok, err)
	}
	if err := src.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("next at end: %v", err)
	}
}

func TestBidSourceBestPriceFirst(t *testing.T) {
	t.Parallel()

	orders := []domain.Order{
		resting(1, domain.SideBuy, 10, 2),
		resting(2, domain.SideBuy, 50, 9),
		resting(3, domain.SideBuy, 50, 3),
		resting(4, domain.SideBuy, 1, 1),
	}
	src, err := NewBidSource(orders)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Sequence{3, 9, 2, 1}
	for i, seq := range want {
		got, ok, err := src.Peek()
		if err != nil || !ok || got.Sequence != seq {
			t.Fatalf("peek %d = seq %d ok %v err %v", i, got.Sequence, ok, err)
		}
		if err := src.Next(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceRejects(t *testing.T) {
	t.Parallel()

	if _, err := NewAskSource([]domain.Order{resting(1, domain.SideBuy, 10, 1)}); !errors.Is(err, ErrSide) {
		t.Fatalf("side: %v", err)
	}
	mixed := []domain.Order{
		resting(1, domain.SideSell, 10, 1),
		resting(2, domain.SideSell, 11, 2),
	}
	mixed[1].MarketID = 2
	if _, err := NewAskSource(mixed); !errors.Is(err, ErrMarket) {
		t.Fatalf("market: %v", err)
	}
	dup := []domain.Order{
		resting(1, domain.SideSell, 10, 1),
		resting(2, domain.SideSell, 10, 1),
	}
	if _, err := NewAskSource(dup); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	empty, err := NewAskSource(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := empty.Peek(); err != nil || ok {
		t.Fatalf("empty ok=%v %v", ok, err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := empty.Peek(); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed peek: %v", err)
	}
}

func resting(id byte, side domain.Side, price, seq uint64) domain.Order {
	return domain.Order{
		ID:                domain.OrderID{31: id},
		Owner:             []byte("maker"),
		MarketID:          1,
		Side:              side,
		Type:              domain.OrderTypeLimit,
		TimeInForce:       domain.TimeInForceGTC,
		Price:             domain.Price(price),
		OriginalQuantity:  10,
		RemainingQuantity: 10,
		Sequence:          domain.Sequence(seq),
	}
}
