package benchmarks

import (
	"encoding/binary"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching/memsource"
)

// Workloads are lot counts at integer ticks. Results are ns/op, allocs/op,
// and bytes/op from the testing harness. This file does not report a throughput figure.

var sink domain.Quantity

func BenchmarkSingleFill(b *testing.B) {
	benchmarkMatch(b, samePriceAsks(1, 10), buy(10, 100), 1)
}

func BenchmarkMakers10(b *testing.B) {
	benchmarkMatch(b, samePriceAsks(10, 10), buy(100, 100), 10)
}

func BenchmarkMakers100(b *testing.B) {
	benchmarkMatch(b, samePriceAsks(100, 10), buy(1000, 100), 100)
}

func BenchmarkMakers1000(b *testing.B) {
	benchmarkMatch(b, samePriceAsks(1000, 10), buy(10_000, 100), 1000)
}

func BenchmarkMultiLevelBook(b *testing.B) {
	const n = 1000
	benchmarkMatch(b, multiLevelAsks(n, 1), buy(uint64(n), 100+uint64(n-1)), n)
}

func BenchmarkPartialFillHeavy(b *testing.B) {
	// 1000 makers of 10 lots. The taker takes 999 full makers and 5 lots of the last.
	const n = 1000
	benchmarkMatch(b, samePriceAsks(n, 10), buy(999*10+5, 100), n)
}

func benchmarkMatch(b *testing.B, makers []domain.Order, incoming domain.Order, wantFills int) {
	b.Helper()
	src, err := memsource.NewAskSource(makers)
	if err != nil {
		b.Fatal(err)
	}
	in := matching.MatchInput{
		Incoming:        incoming,
		ExecutionHeight: 1,
		MaxMakerVisits:  uint32(len(makers) + 1),
	}
	plan, err := matching.Match(src, in)
	if err != nil {
		b.Fatal(err)
	}
	if len(plan.Fills) != wantFills {
		b.Fatalf("fills = %d, want %d", len(plan.Fills), wantFills)
	}
	src.Reset()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plan, err = matching.Match(src, in)
		if err != nil {
			b.Fatal(err)
		}
		sink = plan.RemainingQuantity
		src.Reset()
	}
}

func buy(qty, price uint64) domain.Order {
	return domain.Order{
		ID:                id(1 << 32),
		Owner:             []byte("taker-owner-00000001"),
		MarketID:          1,
		Side:              domain.SideBuy,
		Type:              domain.OrderTypeLimit,
		TimeInForce:       domain.TimeInForceGTC,
		Price:             domain.Price(price),
		OriginalQuantity:  domain.Quantity(qty),
		RemainingQuantity: domain.Quantity(qty),
		CommandNonce:      1,
	}
}

func samePriceAsks(n int, qty uint64) []domain.Order {
	return asks(n, qty, true)
}

func multiLevelAsks(n int, qty uint64) []domain.Order {
	return asks(n, qty, false)
}

func asks(n int, qty uint64, samePrice bool) []domain.Order {
	owner := []byte("maker-owner-00000001")
	out := make([]domain.Order, n)
	for i := 0; i < n; i++ {
		price := uint64(100)
		if !samePrice {
			price += uint64(i)
		}
		out[i] = domain.Order{
			ID:                id(uint64(i + 1)),
			Owner:             owner,
			MarketID:          1,
			Side:              domain.SideSell,
			Type:              domain.OrderTypeLimit,
			TimeInForce:       domain.TimeInForceGTC,
			Price:             domain.Price(price),
			OriginalQuantity:  domain.Quantity(qty),
			RemainingQuantity: domain.Quantity(qty),
			Sequence:          domain.Sequence(i + 1),
		}
	}
	return out
}

func id(n uint64) domain.OrderID {
	var out domain.OrderID
	binary.BigEndian.PutUint64(out[24:], n)
	return out
}
