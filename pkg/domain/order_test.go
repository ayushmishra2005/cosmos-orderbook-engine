package domain

import (
	"errors"
	"math"
	"testing"
)

func TestNextSequence(t *testing.T) {
	t.Parallel()

	next, err := NextSequence(0)
	if err != nil || next != 1 {
		t.Fatalf("first sequence = %d, %v", next, err)
	}
	next, err = NextSequence(41)
	if err != nil || next != 42 {
		t.Fatalf("next sequence = %d, %v", next, err)
	}
	if _, err := NextSequence(math.MaxUint64); !errors.Is(err, ErrSequenceOverflow) {
		t.Fatalf("overflow: %v", err)
	}
}

func TestExpectedCommandNonce(t *testing.T) {
	t.Parallel()

	next, err := ExpectedCommandNonce(0)
	if err != nil || next != 1 {
		t.Fatalf("first nonce = %d, %v", next, err)
	}
	next, err = ExpectedCommandNonce(7)
	if err != nil || next != 8 {
		t.Fatalf("next nonce = %d, %v", next, err)
	}
	if _, err := ExpectedCommandNonce(math.MaxUint64); !errors.Is(err, ErrNonceOverflow) {
		t.Fatalf("overflow: %v", err)
	}
}

func TestOrderValidationAndExpiry(t *testing.T) {
	t.Parallel()

	valid := Order{
		ID:                OrderID{31: 1},
		Owner:             []byte("alice"),
		MarketID:          1,
		Side:              SideBuy,
		Type:              OrderTypeLimit,
		TimeInForce:       TimeInForceGTC,
		Price:             10,
		OriginalQuantity:  5,
		RemainingQuantity: 4,
		CommandNonce:      1,
	}
	if err := valid.ValidateIncoming(); err != nil {
		t.Fatal(err)
	}
	if err := valid.ValidateResting(); !errors.Is(err, ErrInvalidSequence) {
		t.Fatalf("resting without sequence: %v", err)
	}

	resting := valid
	resting.Sequence = 1
	if err := resting.ValidateResting(); err != nil {
		t.Fatal(err)
	}

	gtd := resting
	gtd.TimeInForce = TimeInForceGTD
	gtd.ExpiryHeight = 10
	if gtd.ExpiredAt(9) {
		t.Fatal("GTD should trade before expiry height")
	}
	if !gtd.ExpiredAt(10) {
		t.Fatal("GTD should expire at ExpiryHeight")
	}
	if valid.ExpiredAt(1_000) {
		t.Fatal("GTC must not expire")
	}

	cases := []struct {
		name string
		edit func(*Order)
		err  error
	}{
		{name: "zero id", edit: func(o *Order) { o.ID = OrderID{} }, err: ErrInvalidOrderID},
		{name: "empty owner", edit: func(o *Order) { o.Owner = nil }, err: ErrInvalidOwner},
		{name: "long owner", edit: func(o *Order) { o.Owner = make([]byte, MaxOwnerLength+1) }, err: ErrInvalidOwner},
		{name: "market", edit: func(o *Order) { o.MarketID = 0 }, err: ErrInvalidMarket},
		{name: "side", edit: func(o *Order) { o.Side = 0 }, err: ErrInvalidSide},
		{name: "type", edit: func(o *Order) { o.Type = 9 }, err: ErrInvalidOrderType},
		{name: "tif", edit: func(o *Order) { o.TimeInForce = 0 }, err: ErrInvalidTimeInForce},
		{name: "price", edit: func(o *Order) { o.Price = 0 }, err: ErrInvalidPrice},
		{name: "quantity", edit: func(o *Order) { o.RemainingQuantity = 0 }, err: ErrInvalidQuantity},
		{name: "remaining above original", edit: func(o *Order) { o.RemainingQuantity = 9 }, err: ErrInvalidQuantity},
		{name: "expiry on gtc", edit: func(o *Order) { o.ExpiryHeight = 3 }, err: ErrInvalidExpiry},
		{name: "gtd without expiry", edit: func(o *Order) { o.TimeInForce = TimeInForceGTD }, err: ErrInvalidExpiry},
		{name: "market gtc", edit: func(o *Order) { o.Type = OrderTypeMarket }, err: ErrInvalidTimeInForce},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			o := valid
			tt.edit(&o)
			if err := o.ValidateIncoming(); !errors.Is(err, tt.err) {
				t.Fatalf("got %v, want %v", err, tt.err)
			}
		})
	}

	market := valid
	market.Type = OrderTypeMarket
	market.TimeInForce = TimeInForceIOC
	if err := market.ValidateIncoming(); err != nil {
		t.Fatal(err)
	}
}
