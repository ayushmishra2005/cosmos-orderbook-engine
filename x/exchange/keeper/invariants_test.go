package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func mustInvariant(t *testing.T, k Keeper, ctx sdk.Context) {
	t.Helper()
	if err := k.CheckInvariants(ctx); err != nil {
		t.Fatal(err)
	}
}

func mustDigest(t *testing.T, k Keeper, ctx sdk.Context) [32]byte {
	t.Helper()
	d, err := k.SnapshotDigest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	same, err := k.StateDigestForTest(ctx)
	if err != nil || same != d {
		t.Fatalf("digest %x %x %v", d, same, err)
	}
	return d
}

func TestInvariantsOnTradeAndRemainder(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(1000, 2000))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 20)
	fund(t, k, ctx, buyer, quoteAsset, 10_000)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 10, 0)
	stored := requireIndex(t, k, ctx, sell.OrderID)
	seq := stored.Order.Sequence
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4, 0)
	if len(buy.Fills) != 1 {
		t.Fatal(buy.Fills)
	}
	if err := FillConserves(testMarket(1000, 2000), true, buy.Fills[0]); err != nil {
		t.Fatal(err)
	}
	rest := requireIndex(t, k, ctx, sell.OrderID)
	if rest.Order.Sequence != seq || rest.Order.RemainingQuantity != 6 {
		t.Fatalf("maker %+v", rest.Order)
	}
	if _, err := k.GetOrder(ctx, buy.OrderID); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	mustInvariant(t, k, ctx)

	ioc := mustPlace(t, k, ctx, buyer, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 10, 100, 0)
	if ioc.Rested || len(ioc.Fills) != 1 {
		t.Fatalf("ioc %+v", ioc)
	}
	if _, err := k.GetOrder(ctx, ioc.OrderID); !errors.Is(err, types.ErrNotFound) {
		t.Fatal("ioc rested")
	}
	fund(t, k, ctx, seller, baseAsset, 5)
	market := mustPlace(t, k, ctx, seller, 2, domain.SideSell, domain.OrderTypeMarket, domain.TimeInForceIOC, 1, 1, 0)
	if market.Rested {
		t.Fatal("market rested")
	}
	mustInvariant(t, k, ctx)
}

func TestDigestStableAcrossKeepers(t *testing.T) {
	run := func() [32]byte {
		k, ctx := setup(t)
		mustCreate(t, k, ctx, testMarket(0, 0))
		fund(t, k, ctx, addr(1), baseAsset, 50)
		fund(t, k, ctx, addr(2), quoteAsset, 500)
		mustPlace(t, k, ctx, addr(1), 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 8, 5, 0)
		mustPlace(t, k, ctx, addr(2), 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 8, 2, 0)
		mustInvariant(t, k, ctx)
		return mustDigest(t, k, ctx)
	}
	if run() != run() {
		t.Fatal("snapshot digest changed")
	}
}

func TestCorruptBookAndIndexes(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller := addr(1)
	fund(t, k, ctx, seller, baseAsset, 10)
	res := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 3, 0)
	order, err := k.GetOrder(ctx, res.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	kv := mustKV(t, k, ctx)

	t.Run("book points at missing order", func(t *testing.T) {
		active, err := canonical.EncodeActiveOrderKey(res.OrderID)
		if err != nil {
			t.Fatal(err)
		}
		bz, err := kv.Get(active)
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Delete(active); err != nil {
			t.Fatal(err)
		}
		if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
			t.Fatal(err)
		}
		book, err := bookKey(order.Order)
		if err != nil {
			t.Fatal(err)
		}
		val, err := kv.Get(book)
		if err != nil || !bytes.Equal(val, res.OrderID[:]) {
			t.Fatal("book index was repaired")
		}
		if err := kv.Set(active, bz); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("order without book index", func(t *testing.T) {
		book, err := bookKey(order.Order)
		if err != nil {
			t.Fatal(err)
		}
		val, err := kv.Get(book)
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Delete(book); err != nil {
			t.Fatal(err)
		}
		if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
			t.Fatal(err)
		}
		if _, err := k.GetOrder(ctx, res.OrderID); err != nil {
			t.Fatal(err)
		}
		if err := kv.Set(book, val); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("wrong book price", func(t *testing.T) {
		book, err := bookKey(order.Order)
		if err != nil {
			t.Fatal(err)
		}
		val, err := kv.Get(book)
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Delete(book); err != nil {
			t.Fatal(err)
		}
		bad, err := canonical.EncodeAskKey(marketID, order.Order.Price+1, order.Order.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Set(bad, val); err != nil {
			t.Fatal(err)
		}
		if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
			t.Fatal(err)
		}
		got, err := k.GetOrder(ctx, res.OrderID)
		if err != nil || got.Order.Price != order.Order.Price {
			t.Fatal("order price repaired")
		}
		still, err := kv.Get(bad)
		if err != nil || !bytes.Equal(still, val) {
			t.Fatal("bad book key repaired")
		}
		if err := kv.Delete(bad); err != nil {
			t.Fatal(err)
		}
		if err := kv.Set(book, val); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("wrong owner index", func(t *testing.T) {
		ownerKey, err := canonical.EncodeOwnerOpenOrderKey(seller, marketID, res.OrderID)
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Delete(ownerKey); err != nil {
			t.Fatal(err)
		}
		bad, err := canonical.EncodeOwnerOpenOrderKey(addr(9), marketID, res.OrderID)
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Set(bad, res.OrderID[:]); err != nil {
			t.Fatal(err)
		}
		if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
			t.Fatal(err)
		}
		still, err := kv.Get(bad)
		if err != nil || !bytes.Equal(still, res.OrderID[:]) {
			t.Fatal("owner index repaired")
		}
		if err := kv.Delete(bad); err != nil {
			t.Fatal(err)
		}
		if err := kv.Set(ownerKey, res.OrderID[:]); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("locked balance", func(t *testing.T) {
		bal := bal(t, k, ctx, seller, baseAsset)
		bal.Locked--
		if err := k.setBalance(ctx, seller, baseAsset, bal); err != nil {
			t.Fatal(err)
		}
		if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
			t.Fatal(err)
		}
		if got := balOf(t, k, ctx, seller, baseAsset); got.Locked != bal.Locked {
			t.Fatal("locked balance repaired")
		}
		bal.Locked++
		if err := k.setBalance(ctx, seller, baseAsset, bal); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("duplicate client index", func(t *testing.T) {
		cmd := command(seller, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 9, 1, 0)
		cmd.ClientOrderID = []byte("one")
		second, err := k.PlaceOrder(ctx, cmd)
		if err != nil {
			t.Fatal(err)
		}
		extra, err := canonical.EncodeActiveClientOrderKey(seller, []byte("two"))
		if err != nil {
			t.Fatal(err)
		}
		if err := kv.Set(extra, second.OrderID[:]); err != nil {
			t.Fatal(err)
		}
		if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
			t.Fatal(err)
		}
		still, err := kv.Get(extra)
		if err != nil || !bytes.Equal(still, second.OrderID[:]) {
			t.Fatal("client index repaired")
		}
	})
}

func TestDuplicateClientIDRejected(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller := addr(1)
	fund(t, k, ctx, seller, baseAsset, 10)
	cmd := command(seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	cmd.ClientOrderID = []byte("dup")
	if _, err := k.PlaceOrder(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	before := mustDigest(t, k, ctx)
	cmd.CommandNonce = 2
	if _, err := k.PlaceOrder(ctx, cmd); !errors.Is(err, types.ErrExists) {
		t.Fatal(err)
	}
	if mustDigest(t, k, ctx) != before {
		t.Fatal("duplicate client id changed state")
	}
	mustInvariant(t, k, ctx)
}

func TestSequenceAndNonceRelationships(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller := addr(1)
	fund(t, k, ctx, seller, baseAsset, 5)
	res := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 2, 1, 0)
	key, err := canonical.EncodeMarketSequenceKey(marketID)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.setUint64(ctx, key, 0); err != nil {
		t.Fatal(err)
	}
	if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
		t.Fatal(err)
	}
	got, err := k.GetOrderSequence(ctx, marketID)
	if err != nil || got != 0 {
		t.Fatalf("sequence repaired to %d %v", got, err)
	}
	if err := k.setUint64(ctx, key, 1); err != nil {
		t.Fatal(err)
	}
	nonceKey, err := canonical.EncodeAccountNonceKey(seller)
	if err != nil {
		t.Fatal(err)
	}
	kv := mustKV(t, k, ctx)
	if err := kv.Delete(nonceKey); err != nil {
		t.Fatal(err)
	}
	if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
		t.Fatal(err)
	}
	if _, err := k.GetOrder(ctx, res.OrderID); err != nil {
		t.Fatal(err)
	}
}

func TestCrossedBookAndExpiredNotExecutable(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 5)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 5, 1, 11)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 1, 0)
	later := ctx.WithBlockHeight(11)
	mustInvariant(t, k, later)
	if _, err := k.GetOrder(later, sell.OrderID); err != nil {
		t.Fatal(err)
	}

	stored, err := k.GetOrder(ctx, buy.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	kv := mustKV(t, k, ctx)
	oldBook, err := bookKey(stored.Order)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete(oldBook); err != nil {
		t.Fatal(err)
	}
	stored.Order.Price = 6
	bz, err := types.EncodeOrder(stored)
	if err != nil {
		t.Fatal(err)
	}
	active, err := canonical.EncodeActiveOrderKey(buy.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Set(active, bz); err != nil {
		t.Fatal(err)
	}
	newBook, err := canonical.EncodeBidKey(marketID, 6, stored.Order.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Set(newBook, buy.OrderID[:]); err != nil {
		t.Fatal(err)
	}
	quote := bal(t, k, ctx, buyer, quoteAsset)
	quote.Locked = 6
	if err := k.setBalance(ctx, buyer, quoteAsset, quote); err != nil {
		t.Fatal(err)
	}
	// Height 10 still treats the ask as executable, so bid 6 crosses ask 5.
	if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
		t.Fatal(err)
	}
	got, err := k.GetOrder(ctx, buy.OrderID)
	if err != nil || got.Order.Price != 6 {
		t.Fatal("crossed book repaired")
	}
}

func TestMalformedActiveOrderDetected(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller := addr(1)
	fund(t, k, ctx, seller, baseAsset, 5)
	res := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	kv := mustKV(t, k, ctx)
	active, err := canonical.EncodeActiveOrderKey(res.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	bz, err := kv.Get(active)
	if err != nil {
		t.Fatal(err)
	}
	off := 2 + int(bz[1]) + 8
	bz[off+1] = byte(domain.OrderTypeMarket)
	if err := kv.Set(active, bz); err != nil {
		t.Fatal(err)
	}
	if err := k.CheckInvariants(ctx); !errors.Is(err, types.ErrCorrupt) {
		t.Fatal(err)
	}
	still, err := kv.Get(active)
	if err != nil || still[off+1] != byte(domain.OrderTypeMarket) {
		t.Fatal("market order repaired")
	}
}

func TestFeeInvariants(t *testing.T) {
	one := func(parts int) uint64 {
		k, ctx := setup(t)
		market := testMarket(0, 333_333)
		mustCreate(t, k, ctx, market)
		buyer := addr(9)
		fund(t, k, ctx, buyer, quoteAsset, 1000)
		for i := 0; i < parts; i++ {
			seller := addr(byte(i + 1))
			fund(t, k, ctx, seller, baseAsset, 6)
			qty := uint64(6)
			if parts > 1 {
				qty = 2
			}
			mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, qty, 0)
		}
		buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 6, 0)
		var fee uint64
		for _, fill := range buy.Fills {
			if err := FillConserves(market, true, fill); err != nil {
				t.Fatal(err)
			}
			fee += fill.TakerFee
			if fill.MakerFee != 0 {
				t.Fatal(fill.MakerFee)
			}
		}
		if got := bal(t, k, ctx, types.FeeCollectorOwner, baseAsset).Available; got != fee {
			t.Fatalf("collector %d fee %d", got, fee)
		}
		mustInvariant(t, k, ctx)
		return fee
	}
	if one(1) != one(3) {
		t.Fatal("fee depended on fill partition")
	}

	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	fund(t, k, ctx, addr(1), baseAsset, 5)
	fund(t, k, ctx, addr(2), quoteAsset, 50)
	before := bal(t, k, ctx, types.FeeCollectorOwner, baseAsset)
	mustPlace(t, k, ctx, addr(1), 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	mustPlace(t, k, ctx, addr(2), 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	if bal(t, k, ctx, types.FeeCollectorOwner, baseAsset) != before {
		t.Fatal("zero fee changed the collector")
	}

	k, ctx = setup(t)
	mustCreate(t, k, ctx, testMarket(0, 1))
	fund(t, k, ctx, addr(1), baseAsset, 1)
	fund(t, k, ctx, addr(2), quoteAsset, 100)
	mustPlace(t, k, ctx, addr(1), 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	collector := bal(t, k, ctx, types.FeeCollectorOwner, baseAsset).Available
	beforeDigest := mustDigest(t, k, ctx)
	if _, err := k.PlaceOrder(ctx, command(addr(2), 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 3, 2, 0)); !errors.Is(err, types.ErrFOKRejected) {
		t.Fatal(err)
	}
	if bal(t, k, ctx, types.FeeCollectorOwner, baseAsset).Available != collector || mustDigest(t, k, ctx) != beforeDigest {
		t.Fatal("failed fok accrued fees")
	}

	full := testMarket(1_000_000, 1_000_000)
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	over := full
	over.TakerFeePPM = types.FeeDenominator + 1
	if err := over.Validate(); !errors.Is(err, types.ErrInvalidFee) {
		t.Fatal(err)
	}
	trade := types.Trade{Price: 1, Quantity: 4, BaseAmount: 4, QuoteAmount: 4, TakerFee: 5, MakerFee: 0}
	if err := FillConserves(testMarket(0, 0), true, trade); !errors.Is(err, types.ErrCorrupt) {
		t.Fatal(err)
	}
}

func TestRoleFeesStaySeparate(t *testing.T) {
	k, ctx := setup(t)
	market := testMarket(100_000, 200_000)
	mustCreate(t, k, ctx, market)
	maker, taker := addr(1), addr(2)
	fund(t, k, ctx, maker, baseAsset, 10)
	fund(t, k, ctx, taker, quoteAsset, 1000)
	fund(t, k, ctx, taker, baseAsset, 10)
	sell := mustPlace(t, k, ctx, maker, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 2, 4, 0)
	buy := mustPlace(t, k, ctx, taker, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 2, 6, 0)
	if !buy.Rested || len(buy.Fills) != 1 {
		t.Fatalf("%+v", buy)
	}
	rest, err := k.GetOrder(ctx, buy.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if rest.TakerGross == 0 || rest.MakerGross != 0 {
		t.Fatalf("gross taker %d maker %d", rest.TakerGross, rest.MakerGross)
	}
	hit := mustPlace(t, k, ctx, maker, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 2, 1, 0)
	if len(hit.Fills) != 1 {
		t.Fatal(hit.Fills)
	}
	rest, err = k.GetOrder(ctx, buy.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if rest.MakerGross == 0 || rest.Order.Sequence == 0 {
		t.Fatalf("%+v", rest)
	}
	_ = sell
	var charged uint64
	charged += buy.Fills[0].TakerFee + buy.Fills[0].MakerFee + hit.Fills[0].TakerFee + hit.Fills[0].MakerFee
	baseFee := bal(t, k, ctx, types.FeeCollectorOwner, baseAsset).Available
	quoteFee := bal(t, k, ctx, types.FeeCollectorOwner, quoteAsset).Available
	if baseFee+quoteFee != charged {
		t.Fatalf("collector %d+%d charged %d", baseFee, quoteFee, charged)
	}
	mustInvariant(t, k, ctx)
}

func TestOverflowBoundaries(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	buyer := addr(2)
	fund(t, k, ctx, buyer, quoteAsset, 10)
	before := mustDigest(t, k, ctx)
	if _, err := k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, math.MaxUint64, math.MaxUint64, 0)); !errors.Is(err, arithmetic.ErrOverflow) {
		t.Fatal(err)
	}
	if mustDigest(t, k, ctx) != before {
		t.Fatal("overflow place committed")
	}

	seller := addr(1)
	fund(t, k, ctx, seller, baseAsset, 1)
	key, err := canonical.EncodeMarketSequenceKey(marketID)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.setUint64(ctx, key, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	before = mustDigest(t, k, ctx)
	if _, err := k.PlaceOrder(ctx, command(seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)); !errors.Is(err, domain.ErrSequenceOverflow) {
		t.Fatal(err)
	}
	if mustDigest(t, k, ctx) != before {
		t.Fatal("sequence exhaustion committed")
	}
	if err := k.setUint64(ctx, key, 0); err != nil {
		t.Fatal(err)
	}

	nonceKey, err := canonical.EncodeAccountNonceKey(seller)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.setUint64(ctx, nonceKey, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	before = mustDigest(t, k, ctx)
	if _, err := k.PlaceOrder(ctx, command(seller, math.MaxUint64, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)); !errors.Is(err, domain.ErrNonceOverflow) {
		t.Fatal(err)
	}
	if mustDigest(t, k, ctx) != before {
		t.Fatal("nonce exhaustion committed")
	}

	k, ctx = setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	fund(t, k, ctx, addr(1), baseAsset, 5)
	fund(t, k, ctx, addr(2), quoteAsset, 50)
	mustPlace(t, k, ctx, addr(1), 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 2, 1, 0)
	tradeKey, err := canonical.EncodeTradeSequenceKey(marketID)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.setUint64(ctx, tradeKey, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	before = mustDigest(t, k, ctx)
	if _, err := k.PlaceOrder(ctx, command(addr(2), 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 2, 1, 0)); !errors.Is(err, domain.ErrSequenceOverflow) {
		t.Fatal(err)
	}
	if mustDigest(t, k, ctx) != before {
		t.Fatal("trade sequence exhaustion committed")
	}

	if _, err := arithmetic.Notional(math.MaxUint64, math.MaxUint64, math.MaxUint64); !errors.Is(err, arithmetic.ErrOverflow) {
		t.Fatal(err)
	}
	if _, err := arithmetic.Fee(math.MaxUint64, types.FeeDenominator, types.FeeDenominator); err != nil {
		t.Fatal(err)
	}
}

func TestResourceLimits(t *testing.T) {
	if _, err := pageLimit(maxPageLimit + 1); !errors.Is(err, types.ErrLimit) {
		t.Fatal(err)
	}
	if got, err := pageLimit(maxPageLimit); err != nil || got != maxPageLimit {
		t.Fatal(got, err)
	}
	k, ctx := setup(t)
	if _, err := k.ExpireOrders(ctx, 10, 0); !errors.Is(err, types.ErrBound) {
		t.Fatal(err)
	}

	market := testMarket(0, 0)
	market.MaxMakerVisits = 2
	mustCreate(t, k, ctx, market)
	for i := byte(1); i <= 4; i++ {
		seller := addr(i)
		fund(t, k, ctx, seller, baseAsset, 5)
		mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	}
	buyer := addr(9)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	before := mustDigest(t, k, ctx)
	res := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 10, 0)
	if res.Rested || len(res.Fills) != 2 {
		t.Fatalf("%+v", res)
	}
	if _, err := k.GetOrder(ctx, res.OrderID); !errors.Is(err, types.ErrNotFound) {
		t.Fatal("visit cap left a resting taker")
	}
	if mustDigest(t, k, ctx) == before {
		t.Fatal("visit cap did not fill")
	}
	mustInvariant(t, k, ctx)
}

func TestDeepBookAndExpirationBound(t *testing.T) {
	k, ctx := setup(t)
	market := testMarket(0, 0)
	market.MaxMakerVisits = 64
	mustCreate(t, k, ctx, market)
	for i := 1; i <= 40; i++ {
		seller := addr(byte(i))
		fund(t, k, ctx, seller, baseAsset, 5)
		mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, uint64(50+i), 1, 0)
	}
	buyer := addr(80)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	res := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 1, 0)
	if len(res.Fills) != 0 || !res.Rested {
		t.Fatalf("%+v", res)
	}
	mustInvariant(t, k, ctx)

	k, ctx = setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	for i := 1; i <= 20; i++ {
		seller := addr(byte(i))
		fund(t, k, ctx, seller, baseAsset, 5)
		mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 3, 1, 12)
	}
	n, err := k.ExpireOrders(ctx, 12, 8)
	if err != nil || n != 8 {
		t.Fatal(n, err)
	}
	mustInvariant(t, k, ctx)
	n, err = k.ExpireOrders(ctx, 12, 128)
	if err != nil || n != 12 {
		t.Fatal(n, err)
	}
	mustInvariant(t, k, ctx)
}

func balOf(t *testing.T, k Keeper, ctx sdk.Context, owner []byte, asset domain.AssetID) types.Balance {
	t.Helper()
	return bal(t, k, ctx, owner, asset)
}

type memBank struct {
	bal map[string]uint64
}

func newMemBank() *memBank {
	return &memBank{bal: map[string]uint64{}}
}

func bankKey(addr sdk.AccAddress, denom string) string {
	return string(addr) + "\x00" + denom
}

func (b *memBank) credit(addr sdk.AccAddress, denom string, amount uint64) {
	b.bal[bankKey(addr, denom)] += amount
}

func (b *memBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.Coin{Denom: denom, Amount: sdkmath.NewIntFromUint64(b.bal[bankKey(addr, denom)])}
}

func (b *memBank) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	if recipientModule != types.ModuleName {
		return errors.New("bank: module")
	}
	return b.move(sender, ModuleAddress(), amt)
}

func (b *memBank) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipient sdk.AccAddress, amt sdk.Coins) error {
	if senderModule != types.ModuleName {
		return errors.New("bank: module")
	}
	return b.move(ModuleAddress(), recipient, amt)
}

func (b *memBank) move(from, to sdk.AccAddress, amt sdk.Coins) error {
	next := make(map[string]uint64, len(b.bal))
	for key, value := range b.bal {
		next[key] = value
	}
	for _, coin := range amt {
		if !coin.Amount.IsUint64() || !coin.Amount.IsPositive() {
			return errors.New("bank: amount")
		}
		amount := coin.Amount.Uint64()
		src := bankKey(from, coin.Denom)
		if next[src] < amount {
			return errors.New("bank: insufficient funds")
		}
		next[src] -= amount
		dst := bankKey(to, coin.Denom)
		sum, err := arithmetic.Add(next[dst], amount)
		if err != nil {
			return err
		}
		next[dst] = sum
	}
	b.bal = next
	return nil
}

func TestBankFailuresDoNotDiverge(t *testing.T) {
	k, ctx := setup(t)
	if err := k.initAsset(ctx, types.Asset{ID: baseAsset, Denom: "base"}); err != nil {
		t.Fatal(err)
	}
	if err := k.initAsset(ctx, types.Asset{ID: quoteAsset, Denom: "quote"}); err != nil {
		t.Fatal(err)
	}
	bank := newMemBank()
	k = k.WithBank(bank)
	mustCreate(t, k, ctx, testMarket(0, 0))
	owner := sdk.AccAddress(addr(4))
	bank.credit(owner, "base", 10)
	if err := k.Deposit(ctx, owner, sdk.NewInt64Coin("base", 11)); err == nil {
		t.Fatal("deposited more than the bank balance")
	}
	if bank.bal[bankKey(owner, "base")] != 10 || bal(t, k, ctx, owner, baseAsset) != (types.Balance{}) {
		t.Fatal("insufficient bank deposit diverged")
	}
	if err := k.Deposit(ctx, owner, sdk.NewInt64Coin("base", 7)); err != nil {
		t.Fatal(err)
	}
	if err := k.Withdraw(ctx, owner, sdk.NewInt64Coin("base", 8)); !errors.Is(err, types.ErrInsufficientBalance) {
		t.Fatal(err)
	}
	if bal(t, k, ctx, owner, baseAsset).Available != 7 || bank.bal[bankKey(ModuleAddress(), "base")] != 7 {
		t.Fatal("insufficient internal withdraw diverged")
	}
	if err := k.CheckCustody(ctx); err != nil {
		t.Fatal(err)
	}
	mustInvariant(t, k, ctx)
}
