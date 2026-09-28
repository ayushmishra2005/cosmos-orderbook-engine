package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"

	"cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdkstore "github.com/cosmos/cosmos-sdk/store/v2"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

const (
	baseAsset  domain.AssetID  = 1
	quoteAsset domain.AssetID  = 2
	marketID   domain.MarketID = 1
)

func addr(b byte) []byte {
	return bytes.Repeat([]byte{b}, 20)
}

func setup(t *testing.T) (Keeper, sdk.Context) {
	t.Helper()
	key := storetypes.NewKVStoreKey("exchange")
	tkey := storetypes.NewTransientStoreKey("transient")
	tc := testutil.DefaultContextWithDB(t, key, tkey)
	k, err := NewKeeper(runtime.NewKVStoreService(key), "testing", []byte{0, 0, 0, 0, 0, 0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	return k, tc.Ctx.WithBlockHeight(10)
}

func testMarket(makerPPM, takerPPM uint64) types.Market {
	return types.Market{
		ID:                      marketID,
		BaseAssetID:             baseAsset,
		QuoteAssetID:            quoteAsset,
		BaseLotSize:             1,
		QuoteAtomsPerTickPerLot: 1,
		MakerFeePPM:             makerPPM,
		TakerFeePPM:             takerPPM,
		MaxMakerVisits:          64,
		Enabled:                 true,
	}
}

func mustCreate(t *testing.T, k Keeper, ctx sdk.Context, market types.Market) {
	t.Helper()
	if err := k.CreateMarket(ctx, market); err != nil {
		t.Fatal(err)
	}
}

func fund(t testing.TB, k Keeper, ctx sdk.Context, owner []byte, asset domain.AssetID, available uint64) {
	t.Helper()
	key, err := canonical.EncodeBalanceKey(owner, asset)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := k.kv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Set(key, types.EncodeBalance(types.Balance{Available: available})); err != nil {
		t.Fatal(err)
	}
}

func bal(t *testing.T, k Keeper, ctx sdk.Context, owner []byte, asset domain.AssetID) types.Balance {
	t.Helper()
	got, err := k.GetBalance(ctx, owner, asset)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func nonceOf(t *testing.T, k Keeper, ctx sdk.Context, owner []byte) uint64 {
	t.Helper()
	n, err := k.GetCommandNonce(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustPlace(t *testing.T, k Keeper, ctx sdk.Context, owner []byte, nonce uint64, side domain.Side, typ domain.OrderType, tif domain.TimeInForce, price, qty, expiry uint64) types.PlaceResult {
	t.Helper()
	res, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner:        owner,
		MarketID:     marketID,
		Side:         side,
		Type:         typ,
		TimeInForce:  tif,
		Price:        domain.Price(price),
		Quantity:     domain.Quantity(qty),
		ExpiryHeight: expiry,
		CommandNonce: nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func command(owner []byte, nonce uint64, side domain.Side, typ domain.OrderType, tif domain.TimeInForce, price, qty, expiry uint64) types.PlaceOrderCommand {
	return types.PlaceOrderCommand{
		Owner:        owner,
		MarketID:     marketID,
		Side:         side,
		Type:         typ,
		TimeInForce:  tif,
		Price:        domain.Price(price),
		Quantity:     domain.Quantity(qty),
		ExpiryHeight: expiry,
		CommandNonce: nonce,
	}
}

func requireNoOrder(t *testing.T, k Keeper, ctx sdk.Context, id domain.OrderID) {
	t.Helper()
	_, err := k.GetOrder(ctx, id)
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("order present: %v", err)
	}
}

func requireIndex(t *testing.T, k Keeper, ctx sdk.Context, id domain.OrderID) types.StoredOrder {
	t.Helper()
	order, err := k.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if order.Order.RemainingQuantity > order.Order.OriginalQuantity {
		t.Fatalf("remaining %d exceeds original %d", order.Order.RemainingQuantity, order.Order.OriginalQuantity)
	}
	kv := mustKV(t, k, ctx)
	book, err := bookKey(order.Order)
	if err != nil {
		t.Fatal(err)
	}
	val, err := kv.Get(book)
	if err != nil || !bytes.Equal(val, id[:]) {
		t.Fatalf("book index %x %v", val, err)
	}
	ownerKey, err := canonical.EncodeOwnerOpenOrderKey(order.Order.Owner, order.Order.MarketID, id)
	if err != nil {
		t.Fatal(err)
	}
	val, err = kv.Get(ownerKey)
	if err != nil || !bytes.Equal(val, id[:]) {
		t.Fatalf("owner index %x %v", val, err)
	}
	if order.Order.TimeInForce == domain.TimeInForceGTD {
		exp, err := canonical.EncodeExpirationKey(order.Order.ExpiryHeight, id)
		if err != nil {
			t.Fatal(err)
		}
		val, err = kv.Get(exp)
		if err != nil || !bytes.Equal(val, id[:]) {
			t.Fatalf("expiry index %x %v", val, err)
		}
	}
	asset := quoteAsset
	var want uint64
	if order.Order.Side == domain.SideSell {
		asset = baseAsset
		want, err = arithmetic.BaseAmount(order.Order.RemainingQuantity, 1)
	} else {
		want, err = arithmetic.Notional(order.Order.RemainingQuantity, order.Order.Price, 1)
	}
	if err != nil {
		t.Fatal(err)
	}
	got := bal(t, k, ctx, order.Order.Owner, asset)
	if got.Locked < want {
		t.Fatalf("locked %d < reservation %d", got.Locked, want)
	}
	return order
}

func requireGone(t *testing.T, k Keeper, ctx sdk.Context, order types.StoredOrder) {
	t.Helper()
	requireNoOrder(t, k, ctx, order.Order.ID)
	kv := mustKV(t, k, ctx)
	book, err := bookKey(order.Order)
	if err != nil {
		t.Fatal(err)
	}
	val, err := kv.Get(book)
	if err != nil || val != nil {
		t.Fatalf("book key remains %x %v", val, err)
	}
	ownerKey, err := canonical.EncodeOwnerOpenOrderKey(order.Order.Owner, order.Order.MarketID, order.Order.ID)
	if err != nil {
		t.Fatal(err)
	}
	val, err = kv.Get(ownerKey)
	if err != nil || val != nil {
		t.Fatalf("owner key remains %x %v", val, err)
	}
}

func mustKV(t *testing.T, k Keeper, ctx sdk.Context) store.KVStore {
	t.Helper()
	kv, err := k.kv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

func requireNotCrossed(t *testing.T, k Keeper, ctx sdk.Context) {
	t.Helper()
	height, err := executionHeight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ask, askOK := firstLive(t, k, ctx, domain.SideSell, height)
	bid, bidOK := firstLive(t, k, ctx, domain.SideBuy, height)
	if askOK && bidOK && bid.Price >= ask.Price {
		t.Fatalf("executable book crossed bid %d ask %d", bid.Price, ask.Price)
	}
}

func firstLive(t *testing.T, k Keeper, ctx sdk.Context, side domain.Side, height uint64) (domain.Order, bool) {
	t.Helper()
	src, err := k.openBook(ctx, marketID, side)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for {
		order, ok, err := src.Peek()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return domain.Order{}, false
		}
		if !order.ExpiredAt(height) {
			return order, true
		}
		if err := src.Next(); err != nil {
			t.Fatal(err)
		}
	}
}

func totalAtoms(t *testing.T, k Keeper, ctx sdk.Context, asset domain.AssetID, owners ...[]byte) uint64 {
	t.Helper()
	var sum uint64
	owners = append(owners, types.FeeCollectorOwner)
	for _, owner := range owners {
		b := bal(t, k, ctx, owner, asset)
		next, err := arithmetic.Add(sum, b.Available)
		if err != nil {
			t.Fatal(err)
		}
		next, err = arithmetic.Add(next, b.Locked)
		if err != nil {
			t.Fatal(err)
		}
		sum = next
	}
	return sum
}

func TestSimpleBuySell(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4, 0)
	if len(buy.Fills) != 1 || buy.Fills[0].Quantity != 4 || buy.Fills[0].Price != 5 || buy.Rested {
		t.Fatalf("%+v", buy)
	}
	requireNoOrder(t, k, ctx, sell.OrderID)
	requireNoOrder(t, k, ctx, buy.OrderID)
	if _, err := k.GetOrder(ctx, sell.OrderID); !errors.Is(err, types.ErrNotFound) {
		t.Fatal("filled order still executable")
	}
	if bal(t, k, ctx, buyer, baseAsset).Available != 4 || bal(t, k, ctx, seller, quoteAsset).Available != 20 {
		t.Fatalf("buyer %+v seller %+v", bal(t, k, ctx, buyer, baseAsset), bal(t, k, ctx, seller, quoteAsset))
	}
	again := mustPlace(t, k, ctx, buyer, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 1, 0)
	if len(again.Fills) != 0 || !again.Rested {
		t.Fatal("filled maker executed again")
	}
}

func TestPartialFill(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 10, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4, 0)
	if len(buy.Fills) != 1 || buy.Fills[0].Quantity != 4 || buy.Rested {
		t.Fatalf("%+v", buy)
	}
	got := requireIndex(t, k, ctx, sell.OrderID)
	if got.Order.RemainingQuantity != 6 || got.Order.OriginalQuantity != 10 {
		t.Fatalf("%+v", got.Order)
	}
	if bal(t, k, ctx, seller, baseAsset).Locked != 6 || bal(t, k, ctx, buyer, baseAsset).Available != 4 {
		t.Fatalf("seller %+v buyer %+v", bal(t, k, ctx, seller, baseAsset), bal(t, k, ctx, buyer, baseAsset))
	}
}

func TestMultipleFills(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	a, b, c, buyer := addr(1), addr(2), addr(3), addr(4)
	fund(t, k, ctx, a, baseAsset, 30)
	fund(t, k, ctx, b, baseAsset, 40)
	fund(t, k, ctx, c, baseAsset, 50)
	fund(t, k, ctx, buyer, quoteAsset, 100_000)
	mustPlace(t, k, ctx, a, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 980, 30, 0)
	mustPlace(t, k, ctx, b, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 990, 40, 0)
	rest := mustPlace(t, k, ctx, c, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1000, 50, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1000, 100, 0)
	if len(buy.Fills) != 3 || buy.Rested || buy.Remaining != 0 {
		t.Fatalf("%+v", buy)
	}
	want := []struct {
		qty, price uint64
	}{{30, 980}, {40, 990}, {30, 1000}}
	for i, w := range want {
		if uint64(buy.Fills[i].Quantity) != w.qty || uint64(buy.Fills[i].Price) != w.price {
			t.Fatalf("fill %d %+v", i, buy.Fills[i])
		}
	}
	got := requireIndex(t, k, ctx, rest.OrderID)
	if got.Order.RemainingQuantity != 20 || got.Order.Sequence != 3 {
		t.Fatalf("maker remainder %+v", got.Order)
	}
	if bal(t, k, ctx, buyer, quoteAsset).Available != 1000 || bal(t, k, ctx, buyer, baseAsset).Available != 100 {
		t.Fatalf("buyer quote %+v base %+v", bal(t, k, ctx, buyer, quoteAsset), bal(t, k, ctx, buyer, baseAsset))
	}
	requireNotCrossed(t, k, ctx)
}

func TestMakerPartialRemainder(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 7, 10, 0)
	before := requireIndex(t, k, ctx, sell.OrderID)
	bookBefore, err := bookKey(before.Order)
	if err != nil {
		t.Fatal(err)
	}
	mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 7, 3, 0)
	after := requireIndex(t, k, ctx, sell.OrderID)
	bookAfter, err := bookKey(after.Order)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bookBefore, bookAfter) || after.Order.Sequence != before.Order.Sequence || after.Order.RemainingQuantity != 7 {
		t.Fatalf("before %+v after %+v", before.Order, after.Order)
	}
	if bal(t, k, ctx, seller, baseAsset).Locked != 7 {
		t.Fatal(bal(t, k, ctx, seller, baseAsset))
	}
}

func TestTakerRemainderResting(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 1000)
	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 9, 4, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 9, 10, 0)
	if !buy.Rested || buy.Remaining != 6 || len(buy.Fills) != 1 {
		t.Fatalf("%+v", buy)
	}
	got := requireIndex(t, k, ctx, buy.OrderID)
	if got.Order.Sequence != 2 || bal(t, k, ctx, buyer, quoteAsset).Locked != 54 {
		t.Fatalf("%+v locked %d", got.Order, bal(t, k, ctx, buyer, quoteAsset).Locked)
	}
	requireNotCrossed(t, k, ctx)
}

func TestIOCRemainderCancelled(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 1000)
	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 5, 10, 0)
	if buy.Rested || buy.Remaining != 6 || len(buy.Fills) != 1 {
		t.Fatalf("%+v", buy)
	}
	requireNoOrder(t, k, ctx, buy.OrderID)
	if bal(t, k, ctx, buyer, quoteAsset).Locked != 0 || bal(t, k, ctx, buyer, quoteAsset).Available != 980 {
		t.Fatal(bal(t, k, ctx, buyer, quoteAsset))
	}
	if nonceOf(t, k, ctx, buyer) != 1 {
		t.Fatal(nonceOf(t, k, ctx, buyer))
	}
}

func TestMarketOrder(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 20)
	fund(t, k, ctx, buyer, quoteAsset, 1000)
	fund(t, k, ctx, buyer, baseAsset, 20)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 8, 5, 0)
	seq, err := k.GetOrderSequence(ctx, marketID)
	if err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeMarket, domain.TimeInForceIOC, 8, 5, 0)
	if buy.Rested || len(buy.Fills) != 1 || buy.Fills[0].Price != 8 {
		t.Fatalf("%+v", buy)
	}
	requireNoOrder(t, k, ctx, buy.OrderID)
	requireNoOrder(t, k, ctx, sell.OrderID)
	got, err := k.GetOrderSequence(ctx, marketID)
	if err != nil || got != 1 {
		t.Fatalf("market order took a sequence: %d", got)
	}
	rest := mustPlace(t, k, ctx, buyer, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 8, 5, 0)
	sold := mustPlace(t, k, ctx, seller, 2, domain.SideSell, domain.OrderTypeMarket, domain.TimeInForceFOK, 8, 5, 0)
	if sold.Rested || len(sold.Fills) != 1 {
		t.Fatalf("%+v", sold)
	}
	requireNoOrder(t, k, ctx, sold.OrderID)
	requireNoOrder(t, k, ctx, rest.OrderID)
}

func TestFOKSuccess(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 5, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 4, 5, 0)
	if buy.Rested || buy.Remaining != 0 || len(buy.Fills) != 1 || nonceOf(t, k, ctx, buyer) != 1 {
		t.Fatalf("%+v nonce %d", buy, nonceOf(t, k, ctx, buyer))
	}
}

func TestFOKRollback(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 4, 0)
	rev, err := k.GetRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 4, 5, 0))
	if !errors.Is(err, types.ErrFOKRejected) {
		t.Fatal(err)
	}
	got := requireIndex(t, k, ctx, sell.OrderID)
	if got.Order.RemainingQuantity != 4 || nonceOf(t, k, ctx, buyer) != 0 || bal(t, k, ctx, buyer, quoteAsset).Available != 100 {
		t.Fatalf("%+v nonce %d bal %+v", got.Order, nonceOf(t, k, ctx, buyer), bal(t, k, ctx, buyer, quoteAsset))
	}
	after, err := k.GetRevision(ctx)
	if err != nil || after != rev {
		t.Fatalf("revision %d -> %d", rev, after)
	}
	if _, err := k.GetTrade(ctx, marketID, 1); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSelfTrade(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	alice := addr(1)
	fund(t, k, ctx, alice, baseAsset, 10)
	fund(t, k, ctx, alice, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 5, 0)
	quote := bal(t, k, ctx, alice, quoteAsset)
	buy := mustPlace(t, k, ctx, alice, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 5, 0)
	if len(buy.Fills) != 0 || buy.Rested || buy.Stop != uint8(matching.StopReasonSelfTrade) {
		t.Fatalf("%+v", buy)
	}
	got := requireIndex(t, k, ctx, sell.OrderID)
	if got.Order.RemainingQuantity != 5 || nonceOf(t, k, ctx, alice) != 2 {
		t.Fatalf("%+v", got.Order)
	}
	if bal(t, k, ctx, alice, quoteAsset) != quote {
		t.Fatalf("quote changed %+v -> %+v", quote, bal(t, k, ctx, alice, quoteAsset))
	}
	requireNoOrder(t, k, ctx, buy.OrderID)
}

func TestSelfTradeAfterThirdParty(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	bob, alice := addr(1), addr(2)
	fund(t, k, ctx, bob, baseAsset, 10)
	fund(t, k, ctx, alice, baseAsset, 10)
	fund(t, k, ctx, alice, quoteAsset, 1000)
	bobSell := mustPlace(t, k, ctx, bob, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4, 0)
	aliceSell := mustPlace(t, k, ctx, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4, 0)
	buy := mustPlace(t, k, ctx, alice, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 10, 0)
	if len(buy.Fills) != 1 || buy.Fills[0].MakerOrderID != bobSell.OrderID || buy.Rested || buy.Stop != uint8(matching.StopReasonSelfTrade) {
		t.Fatalf("%+v", buy)
	}
	requireNoOrder(t, k, ctx, bobSell.OrderID)
	got := requireIndex(t, k, ctx, aliceSell.OrderID)
	if got.Order.RemainingQuantity != 4 || bal(t, k, ctx, alice, quoteAsset).Available != 960 || bal(t, k, ctx, alice, quoteAsset).Locked != 0 {
		t.Fatalf("sell %+v quote %+v", got.Order, bal(t, k, ctx, alice, quoteAsset))
	}
	if bal(t, k, ctx, bob, quoteAsset).Available != 40 {
		t.Fatal(bal(t, k, ctx, bob, quoteAsset))
	}
}

func TestBuyPriceImprovement(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 10_000)
	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 90, 10, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 10, 0)
	if len(buy.Fills) != 1 || buy.Fills[0].Price != 90 || buy.Fills[0].QuoteAmount != 900 {
		t.Fatalf("%+v", buy.Fills)
	}
	got := bal(t, k, ctx, buyer, quoteAsset)
	if got.Available != 9100 || got.Locked != 0 || bal(t, k, ctx, buyer, baseAsset).Available != 10 {
		t.Fatalf("buyer quote %+v base %+v", got, bal(t, k, ctx, buyer, baseAsset))
	}
	if bal(t, k, ctx, seller, quoteAsset).Available != 900 {
		t.Fatal(bal(t, k, ctx, seller, quoteAsset))
	}
}

func TestSellCancellation(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 100)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 7, 0)
	stored := requireIndex(t, k, ctx, sell.OrderID)
	res, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: seller, OrderID: sell.OrderID, CommandNonce: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Released != 7 || res.AssetID != baseAsset {
		t.Fatalf("%+v", res)
	}
	requireGone(t, k, ctx, stored)
	if bal(t, k, ctx, seller, baseAsset) != (types.Balance{Available: 100}) || nonceOf(t, k, ctx, seller) != 2 {
		t.Fatal(bal(t, k, ctx, seller, baseAsset))
	}
	rest := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 7, 0)
	if len(rest.Fills) != 0 || !rest.Rested {
		t.Fatal("cancelled order executed")
	}
}

func TestBuyCancellation(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	buyer := addr(2)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 7, 0)
	stored := requireIndex(t, k, ctx, buy.OrderID)
	res, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: buyer, OrderID: buy.OrderID, CommandNonce: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Released != 35 || res.AssetID != quoteAsset {
		t.Fatalf("%+v", res)
	}
	requireGone(t, k, ctx, stored)
	if bal(t, k, ctx, buyer, quoteAsset) != (types.Balance{Available: 100}) {
		t.Fatal(bal(t, k, ctx, buyer, quoteAsset))
	}
}

func TestInsufficientBalance(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	buyer := addr(2)
	fund(t, k, ctx, buyer, quoteAsset, 5)
	rev, err := k.GetRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 1, 0))
	if !errors.Is(err, types.ErrInsufficientBalance) {
		t.Fatal(err)
	}
	after, err := k.GetRevision(ctx)
	if err != nil || after != rev || nonceOf(t, k, ctx, buyer) != 0 || bal(t, k, ctx, buyer, quoteAsset).Available != 5 {
		t.Fatalf("rev %d nonce %d bal %+v", after, nonceOf(t, k, ctx, buyer), bal(t, k, ctx, buyer, quoteAsset))
	}
}

func TestWrongOwnerCancellation(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	alice, bob := addr(1), addr(2)
	fund(t, k, ctx, alice, baseAsset, 10)
	sell := mustPlace(t, k, ctx, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 2, 0)
	_, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: bob, OrderID: sell.OrderID, CommandNonce: 1})
	if !errors.Is(err, types.ErrWrongOwner) {
		t.Fatal(err)
	}
	requireIndex(t, k, ctx, sell.OrderID)
	if nonceOf(t, k, ctx, bob) != 0 || nonceOf(t, k, ctx, alice) != 1 {
		t.Fatalf("bob %d alice %d", nonceOf(t, k, ctx, bob), nonceOf(t, k, ctx, alice))
	}
}

func TestWrongNonce(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	alice := addr(1)
	fund(t, k, ctx, alice, baseAsset, 10)
	_, err := k.PlaceOrder(ctx, command(alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 2, 0))
	if !errors.Is(err, types.ErrWrongNonce) {
		t.Fatal(err)
	}
	if nonceOf(t, k, ctx, alice) != 0 {
		t.Fatal(nonceOf(t, k, ctx, alice))
	}
	sell := mustPlace(t, k, ctx, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 2, 0)
	_, err = k.CancelOrder(ctx, types.CancelOrderCommand{Owner: alice, OrderID: sell.OrderID, CommandNonce: 1})
	if !errors.Is(err, types.ErrWrongNonce) {
		t.Fatal(err)
	}
	requireIndex(t, k, ctx, sell.OrderID)
	if nonceOf(t, k, ctx, alice) != 1 {
		t.Fatal(nonceOf(t, k, ctx, alice))
	}
}

func TestOverflow(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	buyer := addr(2)
	fund(t, k, ctx, buyer, quoteAsset, 10)
	rev, err := k.GetRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, math.MaxUint64, math.MaxUint64, 0))
	if !errors.Is(err, arithmetic.ErrOverflow) {
		t.Fatal(err)
	}
	after, err := k.GetRevision(ctx)
	if err != nil || after != rev || nonceOf(t, k, ctx, buyer) != 0 || bal(t, k, ctx, buyer, quoteAsset).Available != 10 {
		t.Fatalf("rev %d bal %+v", after, bal(t, k, ctx, buyer, quoteAsset))
	}
}

func TestFeeAccounting(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(1, 1))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 10)
	baseBefore := totalAtoms(t, k, ctx, baseAsset, seller, buyer)
	quoteBefore := totalAtoms(t, k, ctx, quoteAsset, seller, buyer)
	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)
	if len(buy.Fills) != 1 || buy.Fills[0].MakerFee != 1 || buy.Fills[0].TakerFee != 1 {
		t.Fatalf("%+v", buy.Fills)
	}
	if bal(t, k, ctx, buyer, baseAsset).Available != 0 || bal(t, k, ctx, seller, quoteAsset).Available != 0 {
		t.Fatalf("buyer base %+v seller quote %+v", bal(t, k, ctx, buyer, baseAsset), bal(t, k, ctx, seller, quoteAsset))
	}
	fcBase := bal(t, k, ctx, types.FeeCollectorOwner, baseAsset)
	fcQuote := bal(t, k, ctx, types.FeeCollectorOwner, quoteAsset)
	if fcBase.Available != 1 || fcBase.Locked != 0 || fcQuote.Available != 1 || fcQuote.Locked != 0 {
		t.Fatalf("collector base %+v quote %+v", fcBase, fcQuote)
	}
	if totalAtoms(t, k, ctx, baseAsset, seller, buyer) != baseBefore || totalAtoms(t, k, ctx, quoteAsset, seller, buyer) != quoteBefore {
		t.Fatal("fees did not conserve assets")
	}
}

func TestSplitFillFeeRounding(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 1))
	a, b, buyer := addr(1), addr(2), addr(3)
	fund(t, k, ctx, a, baseAsset, 1)
	fund(t, k, ctx, b, baseAsset, 1)
	fund(t, k, ctx, buyer, quoteAsset, 10)
	mustPlace(t, k, ctx, a, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)
	mustPlace(t, k, ctx, b, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 2, 0)
	if len(buy.Fills) != 2 || buy.Fills[0].TakerFee != 1 || buy.Fills[1].TakerFee != 0 {
		t.Fatalf("%+v", buy.Fills)
	}
	if bal(t, k, ctx, types.FeeCollectorOwner, baseAsset).Available != 1 || bal(t, k, ctx, buyer, baseAsset).Available != 1 {
		t.Fatalf("collector %+v buyer %+v", bal(t, k, ctx, types.FeeCollectorOwner, baseAsset), bal(t, k, ctx, buyer, baseAsset))
	}

	k2, ctx2 := setup(t)
	mustCreate(t, k2, ctx2, testMarket(1, 0))
	maker, taker := addr(4), addr(5)
	fund(t, k2, ctx2, maker, baseAsset, 3)
	fund(t, k2, ctx2, taker, quoteAsset, 10)
	sell := mustPlace(t, k2, ctx2, maker, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 3, 0)
	first := mustPlace(t, k2, ctx2, taker, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 1, 1, 0)
	second := mustPlace(t, k2, ctx2, taker, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 1, 1, 0)
	if first.Fills[0].MakerFee != 1 || second.Fills[0].MakerFee != 0 {
		t.Fatalf("first %+v second %+v", first.Fills, second.Fills)
	}
	rest, err := k2.GetOrder(ctx2, sell.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if rest.MakerGross != 2 || rest.TakerGross != 0 || rest.Order.RemainingQuantity != 1 {
		t.Fatalf("%+v", rest)
	}
	if bal(t, k2, ctx2, types.FeeCollectorOwner, quoteAsset).Available != 1 {
		t.Fatal(bal(t, k2, ctx2, types.FeeCollectorOwner, quoteAsset))
	}

	k3, ctx3 := setup(t)
	mustCreate(t, k3, ctx3, testMarket(1, 1))
	taker, maker, later := addr(6), addr(7), addr(8)
	fund(t, k3, ctx3, taker, quoteAsset, 10)
	fund(t, k3, ctx3, maker, baseAsset, 1)
	fund(t, k3, ctx3, later, baseAsset, 1)
	mustPlace(t, k3, ctx3, maker, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)
	opened := mustPlace(t, k3, ctx3, taker, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 3, 0)
	if len(opened.Fills) != 1 || opened.Fills[0].TakerFee != 1 || !opened.Rested {
		t.Fatalf("%+v", opened)
	}
	resting, err := k3.GetOrder(ctx3, opened.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if resting.TakerGross != 1 || resting.MakerGross != 0 {
		t.Fatalf("after take %+v", resting)
	}
	made := mustPlace(t, k3, ctx3, later, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceIOC, 1, 1, 0)
	if len(made.Fills) != 1 || made.Fills[0].MakerFee != 1 {
		t.Fatalf("mixed accumulators %+v", made.Fills)
	}
	resting, err = k3.GetOrder(ctx3, opened.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if resting.TakerGross != 1 || resting.MakerGross != 1 || resting.Order.RemainingQuantity != 1 {
		t.Fatalf("after make %+v", resting)
	}
	if bal(t, k3, ctx3, types.FeeCollectorOwner, baseAsset).Available != 2 {
		t.Fatal(bal(t, k3, ctx3, types.FeeCollectorOwner, baseAsset))
	}
}

func TestTradeSequence(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	a, b, buyer := addr(1), addr(2), addr(3)
	fund(t, k, ctx, a, baseAsset, 5)
	fund(t, k, ctx, b, baseAsset, 5)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	mustPlace(t, k, ctx, a, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 2, 1, 0)
	mustPlace(t, k, ctx, b, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 2, 0)
	if len(buy.Fills) != 2 || buy.Fills[0].Sequence != 1 || buy.Fills[1].Sequence != 2 {
		t.Fatalf("%+v", buy.Fills)
	}
	seq, err := k.GetTradeSequence(ctx, marketID)
	if err != nil || seq != 2 {
		t.Fatal(seq, err)
	}
	for i := uint64(1); i <= 2; i++ {
		trade, err := k.GetTrade(ctx, marketID, i)
		if err != nil || trade.Sequence != i {
			t.Fatalf("%d %+v %v", i, trade, err)
		}
	}
	fund(t, k, ctx, a, baseAsset, 5)
	rest := mustPlace(t, k, ctx, buyer, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	if !rest.Rested {
		t.Fatal(rest)
	}
	more := mustPlace(t, k, ctx, a, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceIOC, 3, 1, 0)
	if len(more.Fills) != 1 || more.Fills[0].Sequence != 3 {
		t.Fatalf("%+v", more.Fills)
	}
	if _, err := k.GetTrade(ctx, marketID, 0); !errors.Is(err, domain.ErrInvalidSequence) {
		t.Fatal(err)
	}
}

func TestOrderSequenceAndFIFO(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	a, b, buyer := addr(1), addr(2), addr(3)
	fund(t, k, ctx, a, baseAsset, 10)
	fund(t, k, ctx, b, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	first := mustPlace(t, k, ctx, a, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 2, 0)
	second := mustPlace(t, k, ctx, b, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 2, 0)
	s1 := requireIndex(t, k, ctx, first.OrderID)
	s2 := requireIndex(t, k, ctx, second.OrderID)
	if s1.Order.Sequence != 1 || s2.Order.Sequence != 2 {
		t.Fatalf("%d %d", s1.Order.Sequence, s2.Order.Sequence)
	}
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceIOC, 4, 3, 0)
	if len(buy.Fills) != 2 || buy.Fills[0].MakerOrderID != first.OrderID || buy.Fills[0].Quantity != 2 || buy.Fills[1].MakerOrderID != second.OrderID || buy.Fills[1].Quantity != 1 {
		t.Fatalf("%+v", buy.Fills)
	}
	seq, err := k.GetOrderSequence(ctx, marketID)
	if err != nil || seq != 2 {
		t.Fatalf("full taker consumed a sequence: %d", seq)
	}
	requireNoOrder(t, k, ctx, first.OrderID)
	rest := requireIndex(t, k, ctx, second.OrderID)
	if rest.Order.RemainingQuantity != 1 || rest.Order.Sequence != 2 {
		t.Fatalf("%+v", rest.Order)
	}
	again := mustPlace(t, k, ctx, buyer, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1, 0)
	if !again.Rested || again.Stop != uint8(matching.StopReasonPriceBoundary) {
		t.Fatalf("%+v", again)
	}
	rested := requireIndex(t, k, ctx, again.OrderID)
	if rested.Order.Sequence != 3 {
		t.Fatal(rested.Order.Sequence)
	}
	requireNotCrossed(t, k, ctx)
}

func TestExpiration(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	alice, bob, carol := addr(1), addr(2), addr(3)
	fund(t, k, ctx, alice, baseAsset, 10)
	fund(t, k, ctx, bob, baseAsset, 10)
	fund(t, k, ctx, carol, baseAsset, 10)
	fund(t, k, ctx, bob, quoteAsset, 100)
	due := mustPlace(t, k, ctx, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 5, 4, 15)
	future := mustPlace(t, k, ctx, carol, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 9, 4, 50)
	kv := mustKV(t, k, ctx)
	var height80 [8]byte
	height80[7] = 80
	corrupt := append([]byte{canonical.PrefixExpiration}, height80[:]...)
	corrupt = append(corrupt, 0xFF)
	if err := kv.Set(corrupt, []byte{1}); err != nil {
		t.Fatal(err)
	}

	later := ctx.WithBlockHeight(15)
	buy := mustPlace(t, k, later, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4, 0)
	if len(buy.Fills) != 0 || !buy.Rested {
		t.Fatalf("expired order executed: %+v", buy)
	}
	if bal(t, k, ctx, alice, baseAsset).Locked != 4 {
		t.Fatal("expired order released before ExpireOrders")
	}
	requireNotCrossed(t, k, later)

	if _, err := k.ExpireOrders(ctx, 15, 0); !errors.Is(err, types.ErrBound) {
		t.Fatal(err)
	}
	n, err := k.ExpireOrders(ctx, 15, 10)
	if err != nil || n != 1 {
		t.Fatalf("expired %d %v", n, err)
	}
	requireNoOrder(t, k, ctx, due.OrderID)
	if bal(t, k, ctx, alice, baseAsset) != (types.Balance{Available: 10}) {
		t.Fatal(bal(t, k, ctx, alice, baseAsset))
	}
	requireIndex(t, k, ctx, future.OrderID)
	requireIndex(t, k, ctx, buy.OrderID)
	if bal(t, k, ctx, carol, baseAsset).Locked != 4 {
		t.Fatal(bal(t, k, ctx, carol, baseAsset))
	}

	sameA := mustPlace(t, k, ctx, alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 9, 1, 20)
	sameB := mustPlace(t, k, ctx, bob, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 9, 1, 20)
	small, large := sameA.OrderID, sameB.OrderID
	if bytes.Compare(small[:], large[:]) > 0 {
		small, large = large, small
	}
	n, err = k.ExpireOrders(ctx, 20, 1)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	requireNoOrder(t, k, ctx, small)
	requireIndex(t, k, ctx, large)
	requireIndex(t, k, ctx, future.OrderID)
}

func TestAtomicRollback(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	buyer := addr(2)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	rev, err := k.GetRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k.failBeforeWrite = errors.New("injected failure")
	_, err = k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 3, 0))
	if err == nil || err.Error() != "injected failure" {
		t.Fatal(err)
	}
	after, err := k.GetRevision(ctx)
	if err != nil || after != rev || nonceOf(t, k, ctx, buyer) != 0 || bal(t, k, ctx, buyer, quoteAsset) != (types.Balance{Available: 100}) {
		t.Fatalf("rev %d nonce %d bal %+v err %v", after, nonceOf(t, k, ctx, buyer), bal(t, k, ctx, buyer, quoteAsset), err)
	}
	seq, err := k.GetOrderSequence(ctx, marketID)
	if err != nil || seq != 0 {
		t.Fatal(seq, err)
	}
}

func TestBookIndexConsistency(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 20)
	fund(t, k, ctx, buyer, quoteAsset, 500)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 11, 10, 30)
	stored := requireIndex(t, k, ctx, sell.OrderID)
	bookBefore, err := bookKey(stored.Order)
	if err != nil {
		t.Fatal(err)
	}
	mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 11, 4, 0)
	after := requireIndex(t, k, ctx, sell.OrderID)
	bookAfter, err := bookKey(after.Order)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bookBefore, bookAfter) || after.Order.RemainingQuantity != 6 {
		t.Fatalf("%+v", after.Order)
	}
	res, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: seller, OrderID: sell.OrderID, CommandNonce: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Released != 6 {
		t.Fatal(res.Released)
	}
	requireGone(t, k, ctx, after)
	requireNotCrossed(t, k, ctx)
}

func TestDeterministicPlace(t *testing.T) {
	run := func() (types.PlaceResult, []byte, []byte) {
		t.Helper()
		k, ctx := setup(t)
		mustCreate(t, k, ctx, testMarket(1, 1))
		seller, buyer := addr(1), addr(2)
		fund(t, k, ctx, seller, baseAsset, 10)
		fund(t, k, ctx, buyer, quoteAsset, 1000)
		mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 12, 4, 0)
		res := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 15, 10, 0)
		order, err := k.GetOrder(ctx, res.OrderID)
		if err != nil {
			t.Fatal(err)
		}
		ob, err := types.EncodeOrder(order)
		if err != nil {
			t.Fatal(err)
		}
		tb, err := types.EncodeTrade(res.Fills[0])
		if err != nil {
			t.Fatal(err)
		}
		return res, ob, tb
	}
	a, aOrder, aTrade := run()
	b, bOrder, bTrade := run()
	if a.OrderID != b.OrderID || !bytes.Equal(aOrder, bOrder) || !bytes.Equal(aTrade, bTrade) || a.Remaining != b.Remaining {
		t.Fatalf("divergent results %s %s", a.OrderID, b.OrderID)
	}
}

func TestPartialBuyPriceImprovement(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 1000)
	sell := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 90, 10, 0)
	buy := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 4, 0)
	if len(buy.Fills) != 1 || buy.Fills[0].QuoteAmount != 360 || buy.Rested {
		t.Fatalf("%+v", buy)
	}
	got := bal(t, k, ctx, buyer, quoteAsset)
	if got.Available != 640 || got.Locked != 0 {
		t.Fatalf("%+v", got)
	}
	rest := requireIndex(t, k, ctx, sell.OrderID)
	if rest.Order.RemainingQuantity != 6 {
		t.Fatal(rest.Order.RemainingQuantity)
	}
	requireNotCrossed(t, k, ctx)
}

// Storage benchmarks measure the Cosmos-backed order book. They are not chain throughput.

func BenchmarkStorage(b *testing.B) {
	b.Run("Insert", benchStorageInsert)
	b.Run("Lookup", benchStorageLookup)
	b.Run("BestAsk", benchStorageBestAsk)
	b.Run("BestBid", benchStorageBestBid)
	b.Run("Cancel", benchStorageCancel)
	b.Run("PartialFill", benchStoragePartial)
	b.Run("FullFill", benchStorageFull)
	b.Run("BookQuery", benchStorageBookQuery)
	b.Run("Match/10", func(b *testing.B) { benchStorageMatch(b, 10) })
	b.Run("Match/100", func(b *testing.B) { benchStorageMatch(b, 100) })
	b.Run("Match/1000", func(b *testing.B) { benchStorageMatch(b, 1000) })
}

type storageEnv struct {
	k      Keeper
	ctx    sdk.Context
	counts *benchCounts
	maker  []byte
	taker  []byte
}

func newStorageEnv(b *testing.B) storageEnv {
	b.Helper()
	db := dbm.NewMemDB()
	cms := sdkstore.NewCommitMultiStore(db, log.NewNopLogger())
	key := storetypes.NewKVStoreKey("exchange")
	cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
	if err := cms.LoadLatestVersion(); err != nil {
		b.Fatal(err)
	}
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 10, ChainID: "testing"}, false, log.NewNopLogger())
	counts := &benchCounts{}
	k, err := NewKeeper(counts.Wrap(runtime.NewKVStoreService(key)), "testing", []byte{0, 0, 0, 0, 0, 0, 0, 1})
	if err != nil {
		b.Fatal(err)
	}
	market := testMarket(0, 0)
	market.MaxMakerVisits = 2048
	if err := k.CreateMarket(ctx, market); err != nil {
		b.Fatal(err)
	}
	env := storageEnv{
		k: k, ctx: ctx, counts: counts,
		maker: bytesRepeat(1), taker: bytesRepeat(2),
	}
	env.fund(b, env.maker, baseAsset, 10_000_000)
	env.fund(b, env.maker, quoteAsset, 10_000_000)
	env.fund(b, env.taker, baseAsset, 10_000_000)
	env.fund(b, env.taker, quoteAsset, 10_000_000)
	return env
}

func (e storageEnv) fund(b *testing.B, owner []byte, asset domain.AssetID, amount uint64) {
	b.Helper()
	key, err := canonical.EncodeBalanceKey(owner, asset)
	if err != nil {
		b.Fatal(err)
	}
	kv, err := e.k.kv(e.ctx)
	if err != nil {
		b.Fatal(err)
	}
	if err := kv.Set(key, types.EncodeBalance(types.Balance{Available: amount})); err != nil {
		b.Fatal(err)
	}
}

func (e storageEnv) place(b *testing.B, ctx sdk.Context, owner []byte, nonce uint64, side domain.Side, price, qty uint64) types.PlaceResult {
	b.Helper()
	res, err := e.k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: owner, MarketID: marketID, Side: side,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: domain.Price(price), Quantity: domain.Quantity(qty), CommandNonce: nonce,
	})
	if err != nil {
		b.Fatal(err)
	}
	return res
}

func benchStorageInsert(b *testing.B) {
	env := newStorageEnv(b)
	cmd := types.PlaceOrderCommand{
		Owner: env.taker, MarketID: marketID, Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 10, Quantity: 1, CommandNonce: 1,
	}
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache, _ := env.ctx.CacheContext()
		if _, err := env.k.PlaceOrder(cache, cmd); err != nil {
			b.Fatal(err)
		}
	}
	reportKV(b, env.counts)
}

func benchStorageLookup(b *testing.B) {
	env := newStorageEnv(b)
	res := env.place(b, env.ctx, env.maker, 1, domain.SideSell, 100, 1)
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := env.k.GetOrder(env.ctx, res.OrderID); err != nil {
			b.Fatal(err)
		}
	}
	reportKV(b, env.counts)
}

func benchStorageBestAsk(b *testing.B) {
	env := newStorageEnv(b)
	env.place(b, env.ctx, env.maker, 1, domain.SideSell, 100, 1)
	env.place(b, env.ctx, env.maker, 2, domain.SideSell, 110, 1)
	benchBest(b, env, domain.SideSell)
}

func benchStorageBestBid(b *testing.B) {
	env := newStorageEnv(b)
	env.place(b, env.ctx, env.maker, 1, domain.SideBuy, 90, 1)
	env.place(b, env.ctx, env.maker, 2, domain.SideBuy, 80, 1)
	benchBest(b, env, domain.SideBuy)
}

func benchBest(b *testing.B, env storageEnv, side domain.Side) {
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src, err := env.k.openBook(env.ctx, marketID, side)
		if err != nil {
			b.Fatal(err)
		}
		if _, ok, err := src.Peek(); err != nil || !ok {
			src.Close()
			b.Fatalf("best %v %v", ok, err)
		}
		if err := src.Close(); err != nil {
			b.Fatal(err)
		}
	}
	reportKV(b, env.counts)
}

func benchStorageCancel(b *testing.B) {
	env := newStorageEnv(b)
	res := env.place(b, env.ctx, env.maker, 1, domain.SideBuy, 10, 1)
	cmd := types.CancelOrderCommand{Owner: env.maker, OrderID: res.OrderID, CommandNonce: 2}
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache, _ := env.ctx.CacheContext()
		if _, err := env.k.CancelOrder(cache, cmd); err != nil {
			b.Fatal(err)
		}
	}
	reportKV(b, env.counts)
}

func benchStoragePartial(b *testing.B) {
	env := newStorageEnv(b)
	env.place(b, env.ctx, env.maker, 1, domain.SideSell, 100, 10)
	cmd := types.PlaceOrderCommand{
		Owner: env.taker, MarketID: marketID, Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 100, Quantity: 4, CommandNonce: 1,
	}
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	var fills int
	for i := 0; i < b.N; i++ {
		cache, _ := env.ctx.CacheContext()
		res, err := env.k.PlaceOrder(cache, cmd)
		if err != nil {
			b.Fatal(err)
		}
		fills += len(res.Fills)
	}
	reportKV(b, env.counts)
	if b.N > 0 {
		b.ReportMetric(float64(fills)/float64(b.N), "fills/op")
	}
}

func benchStorageFull(b *testing.B) {
	env := newStorageEnv(b)
	env.place(b, env.ctx, env.maker, 1, domain.SideSell, 100, 10)
	cmd := types.PlaceOrderCommand{
		Owner: env.taker, MarketID: marketID, Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 100, Quantity: 10, CommandNonce: 1,
	}
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache, _ := env.ctx.CacheContext()
		res, err := env.k.PlaceOrder(cache, cmd)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Fills) != 1 || res.Remaining != 0 {
			b.Fatalf("full fill %+v", res)
		}
	}
	reportKV(b, env.counts)
}

func benchStorageBookQuery(b *testing.B) {
	env := newStorageEnv(b)
	other := testMarket(0, 0)
	other.ID = 2
	other.MaxMakerVisits = 64
	if err := env.k.CreateMarket(env.ctx, other); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		env.place(b, env.ctx, env.maker, uint64(i+1), domain.SideBuy, uint64(200-i), 1)
	}
	// Orders on another market must not be visited by a market 1 query.
	for i := 0; i < 50; i++ {
		if _, err := env.k.PlaceOrder(env.ctx, types.PlaceOrderCommand{
			Owner: env.taker, MarketID: 2, Side: domain.SideSell,
			Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
			Price: domain.Price(100 + i), Quantity: 1, CommandNonce: uint64(i + 1),
		}); err != nil {
			b.Fatal(err)
		}
	}
	probe, err := env.k.listBook(env.ctx, marketID, domain.SideBuy, 0, 50)
	if err != nil || len(probe) != 50 {
		b.Fatalf("probe %d %v", len(probe), err)
	}
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := env.k.listBook(env.ctx, marketID, domain.SideBuy, 0, 50)
		if err != nil || len(got) != 50 {
			b.Fatalf("book %d %v", len(got), err)
		}
	}
	reportKV(b, env.counts)
}

func benchStorageMatch(b *testing.B, n int) {
	env := newStorageEnv(b)
	for i := 0; i < n; i++ {
		env.place(b, env.ctx, env.maker, uint64(i+1), domain.SideSell, 100, 1)
	}
	cmd := types.PlaceOrderCommand{
		Owner: env.taker, MarketID: marketID, Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 100, Quantity: domain.Quantity(n), CommandNonce: 1,
	}
	check, _ := env.ctx.CacheContext()
	res, err := env.k.PlaceOrder(check, cmd)
	if err != nil || len(res.Fills) != n {
		b.Fatalf("fills %d err %v", len(res.Fills), err)
	}
	env.counts.Reset()
	b.ReportAllocs()
	b.ResetTimer()
	var fills int
	for i := 0; i < b.N; i++ {
		cache, _ := env.ctx.CacheContext()
		res, err := env.k.PlaceOrder(cache, cmd)
		if err != nil {
			b.Fatal(err)
		}
		fills += len(res.Fills)
	}
	reportKV(b, env.counts)
	if b.N > 0 {
		b.ReportMetric(float64(fills)/float64(b.N), "fills/op")
	}
}

func reportKV(b *testing.B, c *benchCounts) {
	if b.N == 0 {
		return
	}
	b.ReportMetric(float64(c.Reads())/float64(b.N), "kv_reads/op")
	b.ReportMetric(float64(c.Writes())/float64(b.N), "kv_writes/op")
}

func bytesRepeat(b byte) []byte {
	out := make([]byte, 20)
	for i := range out {
		out[i] = b
	}
	return out
}

type benchCounts struct {
	reads  atomic.Uint64
	writes atomic.Uint64
}

// Reset zeroes the counters.
func (c *benchCounts) Reset() {
	if c == nil {
		return
	}
	c.reads.Store(0)
	c.writes.Store(0)
}

// Reads is the number of Get, Has, and iterator entries observed.
func (c *benchCounts) Reads() uint64 {
	if c == nil {
		return 0
	}
	return c.reads.Load()
}

// Writes is the number of Set and Delete calls.
func (c *benchCounts) Writes() uint64 {
	if c == nil {
		return 0
	}
	return c.writes.Load()
}

// Wrap counts operations on the store opened from inner.
func (c *benchCounts) Wrap(inner store.KVStoreService) store.KVStoreService {
	if c == nil || inner == nil {
		return inner
	}
	return benchService{inner: inner, c: c}
}

type benchService struct {
	inner store.KVStoreService
	c     *benchCounts
}

func (s benchService) OpenKVStore(ctx context.Context) store.KVStore {
	return benchKV{inner: s.inner.OpenKVStore(ctx), c: s.c}
}

type benchKV struct {
	inner store.KVStore
	c     *benchCounts
}

func (k benchKV) Get(key []byte) ([]byte, error) {
	k.c.reads.Add(1)
	return k.inner.Get(key)
}

func (k benchKV) Has(key []byte) (bool, error) {
	k.c.reads.Add(1)
	return k.inner.Has(key)
}

func (k benchKV) Set(key, value []byte) error {
	k.c.writes.Add(1)
	return k.inner.Set(key, value)
}

func (k benchKV) Delete(key []byte) error {
	k.c.writes.Add(1)
	return k.inner.Delete(key)
}

func (k benchKV) Iterator(start, end []byte) (store.Iterator, error) {
	it, err := k.inner.Iterator(start, end)
	if err != nil || it == nil {
		return it, err
	}
	return &benchIter{inner: it, c: k.c}, nil
}

func (k benchKV) ReverseIterator(start, end []byte) (store.Iterator, error) {
	it, err := k.inner.ReverseIterator(start, end)
	if err != nil || it == nil {
		return it, err
	}
	return &benchIter{inner: it, c: k.c}, nil
}

type benchIter struct {
	inner   store.Iterator
	c       *benchCounts
	counted bool
}

func (it *benchIter) note() {
	if it.counted || !it.inner.Valid() {
		return
	}
	it.counted = true
	it.c.reads.Add(1)
}

func (it *benchIter) Domain() (start, end []byte) { return it.inner.Domain() }
func (it *benchIter) Valid() bool                 { return it.inner.Valid() }
func (it *benchIter) Next() {
	it.inner.Next()
	it.counted = false
}
func (it *benchIter) Key() []byte {
	it.note()
	return it.inner.Key()
}
func (it *benchIter) Value() []byte {
	it.note()
	return it.inner.Value()
}
func (it *benchIter) Error() error { return it.inner.Error() }
func (it *benchIter) Close() error { return it.inner.Close() }
