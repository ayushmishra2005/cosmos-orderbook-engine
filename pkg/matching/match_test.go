package matching_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching/memsource"
)

var (
	alice = []byte("alice")
	bob   = []byte("bob")
	carol = []byte("carol")
	dave  = []byte("dave")
)

func TestMatch(t *testing.T) {
	t.Parallel()

	example := exampleAsks()
	cases := []struct {
		name     string
		incoming domain.Order
		makers   []domain.Order
		height   uint64
		visits   uint32
		want     matching.MatchPlan
	}{
		{
			name:     "exact_fill",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 25, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 25, 1, 0),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 25, 2, 0),
			},
			want: matching.MatchPlan{
				Fills:      []matching.Fill{fill(1, 9, 100, 25)},
				StopReason: matching.StopReasonFilled,
			},
		},
		{
			name:     "partial_maker_fill",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 30, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 50, 1, 0),
			},
			want: matching.MatchPlan{
				Fills:      []matching.Fill{fill(1, 9, 100, 30)},
				StopReason: matching.StopReasonFilled,
			},
		},
		{
			name:     "partial_taker_fill",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 40, 1, 0),
			},
			want: matching.MatchPlan{
				Fills:             []matching.Fill{fill(1, 9, 100, 40)},
				RemainingQuantity: 60,
				RestIncoming:      true,
				StopReason:        matching.StopReasonBookExhausted,
			},
		},
		{
			// Ticks 980, 990, and 1000 stand for the 9.80 / 9.90 / 10.00 example.
			name:     "multiple_price_levels",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1000, 100, 0, 0),
			makers:   example,
			want: matching.MatchPlan{
				Fills: []matching.Fill{
					fill(1, 9, 980, 30),
					fill(2, 9, 990, 40),
					fill(3, 9, 1000, 30),
				},
				StopReason: matching.StopReasonFilled,
			},
		},
		{
			name:     "same_price_fifo",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 25, 0, 0),
			makers: []domain.Order{
				ord(3, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 3, 0),
				ord(1, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
				ord(2, dave, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 2, 0),
			},
			want: matching.MatchPlan{
				Fills: []matching.Fill{
					fill(1, 9, 100, 10),
					fill(2, 9, 100, 10),
					fill(3, 9, 100, 5),
				},
				StopReason: matching.StopReasonFilled,
			},
		},
		{
			name:     "buy_price_boundary",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 50, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 101, 10, 2, 0),
			},
			want: matching.MatchPlan{
				Fills:             []matching.Fill{fill(1, 9, 100, 10)},
				RemainingQuantity: 40,
				RestIncoming:      true,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name:     "sell_price_boundary",
			incoming: ord(9, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 50, 0, 0),
			makers: []domain.Order{
				ord(2, carol, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 99, 10, 2, 0),
				ord(1, bob, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
			},
			want: matching.MatchPlan{
				Fills:             []matching.Fill{fill(1, 9, 100, 10)},
				RemainingQuantity: 40,
				RestIncoming:      true,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name:     "empty_book",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 0, 0),
			want: matching.MatchPlan{
				RemainingQuantity: 100,
				RestIncoming:      true,
				StopReason:        matching.StopReasonBookExhausted,
			},
		},
		{
			name:     "ioc_remainder",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 100, 100, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 40, 1, 0),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 200, 10, 2, 0),
			},
			want: matching.MatchPlan{
				Fills:             []matching.Fill{fill(1, 9, 100, 40)},
				RemainingQuantity: 60,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name:     "market_order",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeMarket, domain.TimeInForceIOC, 100, 100, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 90, 30, 1, 0),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 40, 2, 0),
				ord(3, dave, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 110, 50, 3, 0),
			},
			want: matching.MatchPlan{
				Fills: []matching.Fill{
					fill(1, 9, 90, 30),
					fill(2, 9, 100, 40),
				},
				RemainingQuantity: 30,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name:     "fok_success",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 1000, 100, 0, 0),
			makers:   example,
			want: matching.MatchPlan{
				Fills: []matching.Fill{
					fill(1, 9, 980, 30),
					fill(2, 9, 990, 40),
					fill(3, 9, 1000, 30),
				},
				StopReason: matching.StopReasonFilled,
			},
		},
		{
			name:     "fok_failure",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 100, 100, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 50, 1, 0),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 100,
				StopReason:        matching.StopReasonFOKRejected,
			},
		},
		{
			name:     "self_trade",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0),
			makers: []domain.Order{
				ord(1, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 10,
				StopReason:        matching.StopReasonSelfTrade,
			},
		},
		{
			name:     "self_trade_after_third_party",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 50, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
				ord(2, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 2, 0),
			},
			want: matching.MatchPlan{
				Fills:             []matching.Fill{fill(1, 9, 100, 10)},
				RemainingQuantity: 40,
				StopReason:        matching.StopReasonSelfTrade,
			},
		},
		{
			name:     "fok_self_trade",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 100, 50, 0, 0),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
				ord(2, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 40, 2, 0),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 50,
				StopReason:        matching.StopReasonFOKRejected,
			},
		},
		{
			name:     "expired_maker",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 0, 0),
			height:   100,
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 100, 30, 1, 100),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 100, 40, 2, 200),
			},
			want: matching.MatchPlan{
				Fills:             []matching.Fill{fill(2, 9, 100, 40)},
				RemainingQuantity: 60,
				RestIncoming:      true,
				StopReason:        matching.StopReasonBookExhausted,
			},
		},
		{
			name:     "incoming_expired",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTD, 100, 100, 0, 100),
			height:   100,
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 1, 0),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 100,
				StopReason:        matching.StopReasonExpired,
			},
		},
		{
			name:     "maker_visit_limit",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 0, 0),
			visits:   2,
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 2, 0),
				ord(3, dave, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 3, 0),
			},
			want: matching.MatchPlan{
				Fills: []matching.Fill{
					fill(1, 9, 100, 10),
					fill(2, 9, 100, 10),
				},
				RemainingQuantity: 80,
				StopReason:        matching.StopReasonVisitLimit,
			},
		},
		{
			name:     "visit_limit_counts_expired",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 0, 0),
			height:   100,
			visits:   1,
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 90, 10, 1, 100),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 100, 10, 2, 200),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 100,
				StopReason:        matching.StopReasonVisitLimit,
			},
		},
		{
			name:     "self_order_outside_limit",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0),
			makers: []domain.Order{
				ord(1, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 200, 10, 1, 0),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 10,
				RestIncoming:      true,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name: "matches_remaining_not_original",
			incoming: func() domain.Order {
				o := ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 0, 0)
				o.RemainingQuantity = 40
				return o
			}(),
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 100, 1, 0),
			},
			want: matching.MatchPlan{
				Fills:      []matching.Fill{fill(1, 9, 100, 40)},
				StopReason: matching.StopReasonFilled,
			},
		},
		{
			name:     "expired_outside_limit_rests",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0),
			height:   100,
			visits:   1,
			makers: []domain.Order{
				ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 200, 10, 1, 100),
				ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 210, 10, 2, 100),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 10,
				RestIncoming:      true,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name:     "expired_bid_outside_limit_rests",
			incoming: ord(9, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0),
			height:   100,
			visits:   1,
			makers: []domain.Order{
				ord(1, bob, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTD, 50, 10, 1, 100),
			},
			want: matching.MatchPlan{
				RemainingQuantity: 10,
				RestIncoming:      true,
				StopReason:        matching.StopReasonPriceBoundary,
			},
		},
		{
			name:     "gtd_rests",
			incoming: ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTD, 100, 15, 0, 50),
			height:   10,
			want: matching.MatchPlan{
				RemainingQuantity: 15,
				RestIncoming:      true,
				StopReason:        matching.StopReasonBookExhausted,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := newBook(t, tc.incoming, tc.makers)
			defer src.Close()
			got, err := matching.Match(src, matching.MatchInput{
				Incoming:        tc.incoming,
				ExecutionHeight: tc.height,
				MaxMakerVisits:  tc.visits,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertPlan(t, tc.incoming, got, tc.want)
			switch tc.name {
			case "partial_maker_fill":
				if 50-got.Fills[0].Quantity != 20 {
					t.Fatalf("maker residual = %d", 50-got.Fills[0].Quantity)
				}
			case "multiple_price_levels", "fok_success":
				if 50-got.Fills[2].Quantity != 20 {
					t.Fatalf("final maker residual = %d", 50-got.Fills[2].Quantity)
				}
			}
		})
	}
}

func TestMatchDeterministicReplay(t *testing.T) {
	t.Parallel()

	incoming := ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1000, 100, 0, 0)
	makers := exampleAsks()
	// Shuffle in the fixture itself is unnecessary: memsource sorts by key,
	// and unique keys have one sorted order.
	var first []byte
	for i := 0; i < 100; i++ {
		src := newBook(t, incoming, makers)
		got, err := matching.Match(src, matching.MatchInput{Incoming: incoming, ExecutionHeight: 7, MaxMakerVisits: 8})
		src.Close()
		if err != nil {
			t.Fatal(err)
		}
		encoded := planBytes(got)
		if i == 0 {
			first = encoded
			continue
		}
		if !bytes.Equal(first, encoded) {
			t.Fatalf("replay %d diverged", i)
		}
	}
}

func TestMatchLeavesUnconsumedMaker(t *testing.T) {
	t.Parallel()

	t.Run("price_boundary", func(t *testing.T) {
		incoming := ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 20, 0, 0)
		makers := []domain.Order{
			ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
			ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 200, 10, 2, 0),
		}
		src := newBook(t, incoming, makers)
		defer src.Close()
		if _, err := matching.Match(src, matching.MatchInput{Incoming: incoming}); err != nil {
			t.Fatal(err)
		}
		next, ok, err := src.Peek()
		if err != nil || !ok || next.ID != oid(2) {
			t.Fatalf("cursor = %+v ok %v %v", next.ID, ok, err)
		}
	})

	t.Run("expired_outside_limit", func(t *testing.T) {
		incoming := ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0)
		makers := []domain.Order{
			ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 200, 10, 1, 100),
		}
		src := newBook(t, incoming, makers)
		defer src.Close()
		got, err := matching.Match(src, matching.MatchInput{Incoming: incoming, ExecutionHeight: 100, MaxMakerVisits: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != matching.StopReasonPriceBoundary || !got.RestIncoming {
			t.Fatalf("plan %+v", got)
		}
		next, ok, err := src.Peek()
		if err != nil || !ok || next.ID != oid(1) {
			t.Fatalf("cursor = %+v ok %v %v", next.ID, ok, err)
		}
	})

	t.Run("self_trade", func(t *testing.T) {
		incoming := ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0)
		makers := []domain.Order{
			ord(1, alice, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
		}
		src := newBook(t, incoming, makers)
		defer src.Close()
		if _, err := matching.Match(src, matching.MatchInput{Incoming: incoming}); err != nil {
			t.Fatal(err)
		}
		next, ok, err := src.Peek()
		if err != nil || !ok || !bytes.Equal(next.Owner, alice) {
			t.Fatalf("self maker not left at cursor: %+v %v", next.Owner, err)
		}
	})
}

func TestMatchErrors(t *testing.T) {
	t.Parallel()

	incoming := ord(9, alice, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0, 0)
	if _, err := matching.Match(nil, matching.MatchInput{Incoming: incoming}); !errors.Is(err, matching.ErrNilSource) {
		t.Fatalf("nil source: %v", err)
	}
	bad := incoming
	bad.Price = 0
	src := newBook(t, incoming, nil)
	defer src.Close()
	if _, err := matching.Match(src, matching.MatchInput{Incoming: bad}); !errors.Is(err, domain.ErrInvalidPrice) {
		t.Fatalf("price: %v", err)
	}

	wrongSide := &listSource{orders: []domain.Order{
		ord(1, bob, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0),
	}}
	if _, err := matching.Match(wrongSide, matching.MatchInput{Incoming: incoming}); !errors.Is(err, matching.ErrMakerSide) {
		t.Fatalf("side: %v", err)
	}
	wrongMarket := ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 1, 0)
	wrongMarket.MarketID = 2
	if _, err := matching.Match(&listSource{orders: []domain.Order{wrongMarket}}, matching.MatchInput{Incoming: incoming}); !errors.Is(err, matching.ErrMakerMarket) {
		t.Fatalf("market: %v", err)
	}
}

func assertPlan(t *testing.T, incoming domain.Order, got, want matching.MatchPlan) {
	t.Helper()
	if got.StopReason != want.StopReason || got.RestIncoming != want.RestIncoming || got.RemainingQuantity != want.RemainingQuantity || len(got.Fills) != len(want.Fills) {
		t.Fatalf("got stop=%s rest=%v rem=%d fills=%d\nwant stop=%s rest=%v rem=%d fills=%d\n%+v",
			got.StopReason, got.RestIncoming, got.RemainingQuantity, len(got.Fills),
			want.StopReason, want.RestIncoming, want.RemainingQuantity, len(want.Fills), got.Fills)
	}
	for i := range want.Fills {
		if got.Fills[i] != want.Fills[i] {
			t.Fatalf("fill %d\n got %+v\nwant %+v", i, got.Fills[i], want.Fills[i])
		}
	}
	var filled uint64
	var err error
	var prev domain.Price
	for i, f := range got.Fills {
		if f.Quantity == 0 || f.TakerOrderID != incoming.ID {
			t.Fatalf("fill %d %+v", i, f)
		}
		if i > 0 {
			if incoming.Side == domain.SideBuy && f.Price < prev {
				t.Fatalf("ask prices decreased")
			}
			if incoming.Side == domain.SideSell && f.Price > prev {
				t.Fatalf("bid prices increased")
			}
		}
		prev = f.Price
		filled, err = arithmetic.Add(filled, uint64(f.Quantity))
		if err != nil {
			t.Fatal(err)
		}
	}
	total, err := arithmetic.Add(filled, uint64(got.RemainingQuantity))
	if err != nil || total != uint64(incoming.RemainingQuantity) {
		t.Fatalf("conserved %d + %d != %d (%v)", filled, got.RemainingQuantity, incoming.RemainingQuantity, err)
	}
	if got.RemainingQuantity == 0 && got.RestIncoming {
		t.Fatal("filled order rested")
	}
	switch got.StopReason {
	case matching.StopReasonSelfTrade, matching.StopReasonVisitLimit, matching.StopReasonFOKRejected, matching.StopReasonExpired:
		if got.RestIncoming {
			t.Fatalf("%s rested", got.StopReason)
		}
	}
	if incoming.Type == domain.OrderTypeMarket || incoming.TimeInForce == domain.TimeInForceIOC || incoming.TimeInForce == domain.TimeInForceFOK {
		if got.RestIncoming {
			t.Fatal("non-resting order rested")
		}
	}
}

func planBytes(p matching.MatchPlan) []byte {
	var buf bytes.Buffer
	var u32 [4]byte
	var u64 [8]byte
	binary.BigEndian.PutUint32(u32[:], uint32(len(p.Fills)))
	buf.Write(u32[:])
	for _, f := range p.Fills {
		buf.Write(f.MakerOrderID[:])
		buf.Write(f.TakerOrderID[:])
		binary.BigEndian.PutUint64(u64[:], uint64(f.Price))
		buf.Write(u64[:])
		binary.BigEndian.PutUint64(u64[:], uint64(f.Quantity))
		buf.Write(u64[:])
	}
	binary.BigEndian.PutUint64(u64[:], uint64(p.RemainingQuantity))
	buf.Write(u64[:])
	if p.RestIncoming {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	buf.WriteByte(byte(p.StopReason))
	return buf.Bytes()
}

func newBook(t *testing.T, incoming domain.Order, makers []domain.Order) *memsource.Source {
	t.Helper()
	var (
		src *memsource.Source
		err error
	)
	if incoming.Side == domain.SideSell {
		src, err = memsource.NewBidSource(makers)
	} else {
		src, err = memsource.NewAskSource(makers)
	}
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func exampleAsks() []domain.Order {
	return []domain.Order{
		ord(1, bob, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 980, 30, 1, 0),
		ord(2, carol, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 990, 40, 2, 0),
		ord(3, dave, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1000, 50, 3, 0),
	}
}

func ord(id byte, owner []byte, side domain.Side, typ domain.OrderType, tif domain.TimeInForce, price, qty, seq, expiry uint64) domain.Order {
	return domain.Order{
		ID:                oid(id),
		Owner:             owner,
		MarketID:          1,
		Side:              side,
		Type:              typ,
		TimeInForce:       tif,
		Price:             domain.Price(price),
		OriginalQuantity:  domain.Quantity(qty),
		RemainingQuantity: domain.Quantity(qty),
		Sequence:          domain.Sequence(seq),
		ExpiryHeight:      expiry,
		CommandNonce:      1,
	}
}

func oid(n byte) domain.OrderID {
	var id domain.OrderID
	id[31] = n
	return id
}

func fill(maker, taker byte, price, qty uint64) matching.Fill {
	return matching.Fill{
		MakerOrderID: oid(maker),
		TakerOrderID: oid(taker),
		Price:        domain.Price(price),
		Quantity:     domain.Quantity(qty),
	}
}

type listSource struct {
	orders []domain.Order
	i      int
}

func (s *listSource) Peek() (domain.Order, bool, error) {
	if s.i >= len(s.orders) {
		return domain.Order{}, false, nil
	}
	return s.orders[s.i], true, nil
}

func (s *listSource) Next() error {
	if s.i >= len(s.orders) {
		return errors.New("exhausted")
	}
	s.i++
	return nil
}

func (s *listSource) Close() error { return nil }
