package keeper

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestClientIDRemovalDoesNotScan(t *testing.T) {
	stat := &clientStat{}
	k, ctx := setupCounted(t, stat)
	mustCreate(t, k, ctx, testMarket(0, 0))
	other := testMarket(0, 0)
	other.ID = 2
	mustCreate(t, k, ctx, other)
	owner := addr(1)
	fund(t, k, ctx, owner, baseAsset, 10_000)
	fund(t, k, ctx, owner, quoteAsset, 10_000)

	const n = 24
	ids := make([]domain.OrderID, n)
	for i := 0; i < n; i++ {
		market := marketID
		side := domain.SideSell
		asset := baseAsset
		if i%2 == 1 {
			market = 2
			side = domain.SideBuy
			asset = quoteAsset
		}
		_ = asset
		res, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
			Owner: owner, MarketID: market, Side: side,
			Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
			Price: 100, Quantity: 1, CommandNonce: uint64(i + 1),
			ClientOrderID: []byte("cid-" + strconv.Itoa(i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = res.OrderID
	}
	stat.iters.Store(0)
	stat.dels.Store(0)
	if _, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: owner, OrderID: ids[3], CommandNonce: uint64(n + 1)}); err != nil {
		t.Fatal(err)
	}
	if stat.iters.Load() != 0 || stat.dels.Load() != 1 {
		t.Fatalf("client iters %d deletes %d", stat.iters.Load(), stat.dels.Load())
	}
	for i, id := range ids {
		if i == 3 {
			continue
		}
		key, err := canonical.EncodeActiveClientOrderKey(owner, []byte("cid-"+strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := k.get(ctx, key)
		if err != nil || len(got) == 0 || string(got) != string(id[:]) {
			t.Fatalf("client %d missing", i)
		}
	}
}

func TestClientIDLifecycle(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 100)
	fund(t, k, ctx, buyer, quoteAsset, 1000)

	stat := &clientStat{}
	k2, ctx2 := setupCounted(t, stat)
	mustCreate(t, k2, ctx2, testMarket(0, 0))
	fund(t, k2, ctx2, seller, baseAsset, 10)
	res, err := k2.PlaceOrder(ctx2, types.PlaceOrderCommand{
		Owner: seller, MarketID: marketID, Side: domain.SideSell,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 40, Quantity: 1, CommandNonce: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	stat.iters.Store(0)
	stat.dels.Store(0)
	if _, err := k2.CancelOrder(ctx2, types.CancelOrderCommand{Owner: seller, OrderID: res.OrderID, CommandNonce: 2}); err != nil {
		t.Fatal(err)
	}
	if stat.iters.Load() != 0 || stat.dels.Load() != 0 {
		t.Fatalf("no client id iters %d dels %d", stat.iters.Load(), stat.dels.Load())
	}

	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 40, 1, 0)
	partial, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: seller, MarketID: marketID, Side: domain.SideSell,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 10, Quantity: 10, CommandNonce: 2, ClientOrderID: []byte("desk"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: buyer, MarketID: marketID, Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 10, Quantity: 4, CommandNonce: 1,
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := k.GetOrder(ctx, partial.OrderID)
	if err != nil || string(stored.ClientOrderID) != "desk" || stored.Order.RemainingQuantity != 6 {
		t.Fatalf("partial %+v %v", stored, err)
	}
	key, err := canonical.EncodeActiveClientOrderKey(seller, []byte("desk"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := k.get(ctx, key); err != nil || string(got) != string(partial.OrderID[:]) {
		t.Fatal("client index missing after partial fill")
	}
	if _, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: buyer, MarketID: marketID, Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 10, Quantity: 6, CommandNonce: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.GetOrder(ctx, partial.OrderID); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	if got, err := k.get(ctx, key); err != nil || got != nil {
		t.Fatal("client index survived full fill")
	}
	again, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: seller, MarketID: marketID, Side: domain.SideSell,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 11, Quantity: 1, CommandNonce: 3, ClientOrderID: []byte("desk"),
	})
	if err != nil || !again.Rested {
		t.Fatalf("reuse %v %+v", err, again)
	}

	cancelID := again.OrderID
	if _, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: seller, OrderID: cancelID, CommandNonce: 4}); err != nil {
		t.Fatal(err)
	}
	if got, err := k.get(ctx, key); err != nil || got != nil {
		t.Fatal("client index survived cancel")
	}

	gtd, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: seller, MarketID: marketID, Side: domain.SideSell,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTD,
		Price: 12, Quantity: 1, ExpiryHeight: 30, CommandNonce: 5, ClientOrderID: []byte("gtd"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.ExpireOrders(ctx, 30, 8); err != nil {
		t.Fatal(err)
	}
	gtdKey, err := canonical.EncodeActiveClientOrderKey(seller, []byte("gtd"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := k.get(ctx, gtdKey); err != nil || got != nil {
		t.Fatal("client index survived expiry")
	}
	if _, err := k.GetOrder(ctx, gtd.OrderID); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
}

func BenchmarkCancelClientIDs(b *testing.B) {
	for _, n := range []int{1, 64} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			counts := &benchCounts{}
			k, ctx := setupBench(b, counts)
			if err := k.CreateMarket(ctx, testMarket(0, 0)); err != nil {
				b.Fatal(err)
			}
			owner := addr(1)
			fund(b, k, ctx, owner, baseAsset, uint64(n+2))
			var target domain.OrderID
			for i := 0; i < n; i++ {
				res, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
					Owner: owner, MarketID: marketID, Side: domain.SideSell,
					Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
					Price: 10, Quantity: 1, CommandNonce: uint64(i + 1),
					ClientOrderID: []byte("b-" + strconv.Itoa(i)),
				})
				if err != nil {
					b.Fatal(err)
				}
				if i == 0 {
					target = res.OrderID
				}
			}
			cmd := types.CancelOrderCommand{Owner: owner, OrderID: target, CommandNonce: uint64(n + 1)}
			counts.Reset()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cache, _ := ctx.CacheContext()
				if _, err := k.CancelOrder(cache, cmd); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(counts.Reads())/float64(b.N), "kv_reads/op")
		})
	}
}

func setupCounted(t *testing.T, stat *clientStat) (Keeper, sdk.Context) {
	t.Helper()
	key := storetypes.NewKVStoreKey("exchange")
	tkey := storetypes.NewTransientStoreKey("transient")
	tc := testutil.DefaultContextWithDB(t, key, tkey)
	k, err := NewKeeper(stat.Wrap(runtime.NewKVStoreService(key)), "testing", []byte{0, 0, 0, 0, 0, 0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	return k, tc.Ctx.WithBlockHeight(10)
}

func setupBench(b *testing.B, counts *benchCounts) (Keeper, sdk.Context) {
	b.Helper()
	key := storetypes.NewKVStoreKey("exchange")
	tkey := storetypes.NewTransientStoreKey("transient")
	tc := testutil.DefaultContextWithDB(b, key, tkey)
	k, err := NewKeeper(counts.Wrap(runtime.NewKVStoreService(key)), "testing", []byte{0, 0, 0, 0, 0, 0, 0, 1})
	if err != nil {
		b.Fatal(err)
	}
	return k, tc.Ctx.WithBlockHeight(10)
}

type clientStat struct {
	iters atomic.Int64
	dels  atomic.Int64
}

func (s *clientStat) Wrap(inner store.KVStoreService) store.KVStoreService {
	return clientService{inner: inner, st: s}
}

type clientService struct {
	inner store.KVStoreService
	st    *clientStat
}

func (s clientService) OpenKVStore(ctx context.Context) store.KVStore {
	return clientKV{inner: s.inner.OpenKVStore(ctx), st: s.st}
}

type clientKV struct {
	inner store.KVStore
	st    *clientStat
}

func (k clientKV) Get(key []byte) ([]byte, error) { return k.inner.Get(key) }
func (k clientKV) Has(key []byte) (bool, error)   { return k.inner.Has(key) }
func (k clientKV) Set(key, value []byte) error    { return k.inner.Set(key, value) }
func (k clientKV) Delete(key []byte) error {
	if len(key) > 0 && key[0] == canonical.PrefixActiveClientOrder {
		k.st.dels.Add(1)
	}
	return k.inner.Delete(key)
}
func (k clientKV) Iterator(start, end []byte) (store.Iterator, error) {
	if len(start) > 0 && start[0] == canonical.PrefixActiveClientOrder {
		k.st.iters.Add(1)
	}
	return k.inner.Iterator(start, end)
}
func (k clientKV) ReverseIterator(start, end []byte) (store.Iterator, error) {
	return k.inner.ReverseIterator(start, end)
}
