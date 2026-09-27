package matching_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching/memsource"
)

func FuzzMatchReplay(f *testing.F) {
	f.Add(uint8(0), uint8(0), uint64(1000), uint64(100), uint8(3), uint64(30), uint64(10), uint32(10), false)
	f.Add(uint8(1), uint8(2), uint64(100), uint64(40), uint8(2), uint64(50), uint64(1), uint32(0), true)
	f.Fuzz(func(t *testing.T, sideN, tifN uint8, price, qty uint64, n uint8, makerQty, height uint64, visits uint32, self bool) {
		if price == 0 || qty == 0 || qty > 100_000 || makerQty == 0 || makerQty > 100_000 {
			return
		}
		if n == 0 {
			n = 1
		}
		if n > 8 {
			n = 8
		}
		side := domain.SideBuy
		if sideN%2 == 1 {
			side = domain.SideSell
		}
		tif, expiry := fuzzTIF(tifN, height)
		if tif == 0 {
			return
		}
		takerPrice := price
		if side == domain.SideBuy && takerPrice < uint64(n) {
			takerPrice = uint64(n)
		}
		if side == domain.SideSell && takerPrice > math.MaxUint64-uint64(n) {
			takerPrice = math.MaxUint64 - uint64(n)
		}
		if takerPrice == 0 {
			return
		}

		takerOwner := []byte{0x01, 0x02, 0x03, 0x04}
		makerOwner := []byte{0x0A, 0x0B, 0x0C, 0x0D}
		incoming := domain.Order{
			ID:                fuzzID(100),
			Owner:             takerOwner,
			MarketID:          1,
			Side:              side,
			Type:              domain.OrderTypeLimit,
			TimeInForce:       tif,
			Price:             domain.Price(takerPrice),
			OriginalQuantity:  domain.Quantity(qty),
			RemainingQuantity: domain.Quantity(qty),
			ExpiryHeight:      expiry,
			CommandNonce:      1,
		}
		makers := make([]domain.Order, n)
		makerSide := domain.SideSell
		if side == domain.SideSell {
			makerSide = domain.SideBuy
		}
		for i := uint8(0); i < n; i++ {
			var p uint64
			if side == domain.SideBuy {
				p = takerPrice - uint64(n-1-i)
			} else {
				p = takerPrice + uint64(i)
			}
			owner := makerOwner
			if self && ((side == domain.SideBuy && i == n-1) || (side == domain.SideSell && i == 0)) {
				owner = takerOwner
			}
			makers[i] = domain.Order{
				ID:                fuzzID(uint64(i) + 1),
				Owner:             owner,
				MarketID:          1,
				Side:              makerSide,
				Type:              domain.OrderTypeLimit,
				TimeInForce:       domain.TimeInForceGTC,
				Price:             domain.Price(p),
				OriginalQuantity:  domain.Quantity(makerQty),
				RemainingQuantity: domain.Quantity(makerQty),
				Sequence:          domain.Sequence(i) + 1,
				CommandNonce:      1,
			}
		}

		run := func() (matching.MatchPlan, error) {
			var (
				src *memsource.Source
				err error
			)
			if side == domain.SideSell {
				src, err = memsource.NewBidSource(makers)
			} else {
				src, err = memsource.NewAskSource(makers)
			}
			if err != nil {
				return matching.MatchPlan{}, err
			}
			defer src.Close()
			return matching.Match(src, matching.MatchInput{
				Incoming:        incoming,
				ExecutionHeight: height,
				MaxMakerVisits:  visits,
			})
		}
		a, errA := run()
		b, errB := run()
		if (errA == nil) != (errB == nil) || (errA != nil && errA.Error() != errB.Error()) {
			t.Fatalf("error mismatch %v / %v", errA, errB)
		}
		if errA != nil {
			return
		}
		if !bytes.Equal(planBytes(a), planBytes(b)) {
			t.Fatalf("plans diverged\n%+v\n%+v", a, b)
		}
		var sum uint64
		var prev domain.Price
		for i, fill := range a.Fills {
			if fill.Quantity == 0 || fill.TakerOrderID != incoming.ID {
				t.Fatalf("bad fill %+v", fill)
			}
			if i > 0 {
				if side == domain.SideBuy && fill.Price < prev {
					t.Fatal("ask price decreased")
				}
				if side == domain.SideSell && fill.Price > prev {
					t.Fatal("bid price increased")
				}
			}
			prev = fill.Price
			sum += uint64(fill.Quantity)
		}
		if sum+uint64(a.RemainingQuantity) != qty {
			t.Fatalf("quantity %d + %d != %d", sum, a.RemainingQuantity, qty)
		}
		if a.StopReason == matching.StopReasonFOKRejected && (len(a.Fills) != 0 || a.RemainingQuantity != domain.Quantity(qty)) {
			t.Fatalf("fok plan %+v", a)
		}
		if (tif == domain.TimeInForceIOC || tif == domain.TimeInForceFOK) && a.RestIncoming {
			t.Fatal("rested non-resting tif")
		}
	})
}

func fuzzTIF(n uint8, height uint64) (domain.TimeInForce, uint64) {
	switch n % 4 {
	case 0:
		return domain.TimeInForceGTC, 0
	case 1:
		return domain.TimeInForceIOC, 0
	case 2:
		return domain.TimeInForceFOK, 0
	default:
		if height == math.MaxUint64 {
			return 0, 0
		}
		return domain.TimeInForceGTD, height + 1
	}
}

func fuzzID(n uint64) domain.OrderID {
	var id domain.OrderID
	binaryPut(id[:], n)
	return id
}

func binaryPut(dst []byte, n uint64) {
	for i := 0; i < 8; i++ {
		dst[len(dst)-1-i] = byte(n >> (8 * i))
	}
}
