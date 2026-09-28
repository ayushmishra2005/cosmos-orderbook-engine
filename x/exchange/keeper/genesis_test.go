package keeper

import (
	"context"
	"errors"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestInitGenesisWritesNothingOnFailure(t *testing.T) {
	k, ctx := setupGenesisKeeper(t, staticBank{amount: sdkmath.NewInt(0)})
	before := mustDigest(t, k, ctx)

	if err := k.InitGenesis(ctx, v1.GenesisState{InstanceId: "nope"}); err == nil {
		t.Fatal("accepted bad instance")
	}
	if got := mustDigest(t, k, ctx); got != before {
		t.Fatal("validation failure wrote state")
	}

	plain, plainCtx := setup(t)
	before = mustDigest(t, plain, plainCtx)
	gs := *v1.DefaultGenesis()
	gs.Balances = []*v1.GenesisBalance{{
		Owner: sdk.AccAddress(addr(9)).String(), AssetId: 1, Available: 10,
	}}
	if err := plain.InitGenesis(plainCtx, gs); !errors.Is(err, types.ErrInstance) {
		t.Fatalf("instance: %v", err)
	}
	if got := mustDigest(t, plain, plainCtx); got != before {
		t.Fatal("instance mismatch wrote state")
	}

	before = mustDigest(t, k, ctx)
	bad := *v1.DefaultGenesis()
	bad.Balances = []*v1.GenesisBalance{{
		Owner: sdk.AccAddress(addr(9)).String(), AssetId: 1, Locked: 4,
	}}
	if err := k.InitGenesis(ctx, bad); !errors.Is(err, types.ErrCorrupt) {
		t.Fatalf("unlocked order: %v", err)
	}
	if got := mustDigest(t, k, ctx); got != before {
		t.Fatal("corrupt genesis wrote state")
	}

	backed, backedCtx := setupGenesisKeeper(t, staticBank{amount: sdkmath.NewInt(1 << 32)})
	before = mustDigest(t, backed, backedCtx)
	unhashed := *v1.DefaultGenesis()
	owner := sdk.AccAddress(addr(4)).String()
	var id [32]byte
	id[0] = 7
	unhashed.Orders = []*v1.GenesisOrder{{
		OrderId: id[:], Owner: owner, MarketId: 1,
		Side: v1.Side_SIDE_SELL, OrderType: v1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: v1.TimeInForce_TIME_IN_FORCE_GTC,
		PriceTicks:  1, OriginalLots: 1, RemainingLots: 1, Sequence: 1, CommandNonce: 1,
	}}
	unhashed.Balances = []*v1.GenesisBalance{{Owner: owner, AssetId: 1, Locked: 1}}
	unhashed.Nonces = []*v1.AccountNonce{{Owner: owner, Nonce: 1}}
	unhashed.OrderSequences = []*v1.MarketSequence{{MarketId: 1, Sequence: 1}}
	if err := unhashed.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := backed.InitGenesis(backedCtx, unhashed); !errors.Is(err, domain.ErrInvalidOrderID) {
		t.Fatalf("order id: %v", err)
	}
	if got := mustDigest(t, backed, backedCtx); got != before {
		t.Fatal("bad order id wrote state")
	}

	before = mustDigest(t, k, ctx)
	thin := *v1.DefaultGenesis()
	thin.Balances = []*v1.GenesisBalance{{
		Owner: sdk.AccAddress(addr(3)).String(), AssetId: 1, Available: 10,
	}}
	if err := k.InitGenesis(ctx, thin); !errors.Is(err, types.ErrUnbacked) {
		t.Fatalf("backing: %v", err)
	}
	if got := mustDigest(t, k, ctx); got != before {
		t.Fatal("unbacked genesis wrote state")
	}
}

func TestGenesisKeeperRoundTrip(t *testing.T) {
	bank := staticBank{amount: sdkmath.NewInt(1 << 32)}
	k, ctx := setupGenesisKeeper(t, bank)
	if err := k.initAsset(ctx, types.Asset{ID: baseAsset, Denom: "base"}); err != nil {
		t.Fatal(err)
	}
	if err := k.initAsset(ctx, types.Asset{ID: quoteAsset, Denom: "quote"}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, k, ctx, testMarket(1, 1))
	second := testMarket(1, 1)
	second.ID = 2
	mustCreate(t, k, ctx, second)
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	opened, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: seller, MarketID: marketID, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 10, Quantity: 3, CommandNonce: 1,
		ClientOrderID: []byte("desk"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: buyer, MarketID: marketID, Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 10, Quantity: 1, CommandNonce: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: buyer, MarketID: marketID, Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 9, Quantity: 1, CommandNonce: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner: seller, MarketID: 2, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTD, Price: 12, Quantity: 1, ExpiryHeight: 40, CommandNonce: 2,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := k.ExportGenesis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondExport, err := k.ExportGenesis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(&first, &secondExport) {
		t.Fatal("export order changed")
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	want, err := k.SnapshotDigest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := k.GetOrder(ctx, opened.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	k2, ctx2 := setupGenesisKeeper(t, bank)
	if err := k2.InitGenesis(ctx2, first); err != nil {
		t.Fatal(err)
	}
	got, err := k2.SnapshotDigest(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if want != got {
		t.Fatal("round trip digest changed")
	}
	if err := k2.CheckInvariants(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := k2.CheckCustody(ctx2); err != nil {
		t.Fatal(err)
	}
	imported, err := k2.GetOrder(ctx2, opened.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if imported.MakerGross != rest.MakerGross || imported.TakerGross != rest.TakerGross || imported.Order.RemainingQuantity != 2 || string(imported.ClientOrderID) != "desk" {
		t.Fatalf("gross %+v", imported)
	}
	_, err = k2.PlaceOrder(ctx2, types.PlaceOrderCommand{
		Owner: seller, MarketID: marketID, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 11, Quantity: 1, CommandNonce: 3,
		ClientOrderID: []byte("desk"),
	})
	if !errors.Is(err, types.ErrExists) {
		t.Fatalf("client id: %v", err)
	}
}

func setupGenesisKeeper(t *testing.T, bank BankKeeper) (Keeper, sdk.Context) {
	t.Helper()
	key := storetypes.NewKVStoreKey("exchange")
	tkey := storetypes.NewTransientStoreKey("transient")
	tc := testutil.DefaultContextWithDB(t, key, tkey)
	k, err := NewKeeper(runtime.NewKVStoreService(key), "testing", []byte(v1.DefaultInstanceID))
	if err != nil {
		t.Fatal(err)
	}
	if bank != nil {
		k = k.WithBank(bank)
	}
	return k, tc.Ctx.WithBlockHeight(10)
}

type staticBank struct {
	amount sdkmath.Int
}

func (b staticBank) GetBalance(_ context.Context, _ sdk.AccAddress, denom string) sdk.Coin {
	if b.amount.IsNil() || b.amount.IsZero() {
		return sdk.NewInt64Coin(denom, 0)
	}
	return sdk.NewCoin(denom, b.amount)
}

func (staticBank) SendCoinsFromAccountToModule(context.Context, sdk.AccAddress, string, sdk.Coins) error {
	return nil
}

func (staticBank) SendCoinsFromModuleToAccount(context.Context, string, sdk.AccAddress, sdk.Coins) error {
	return nil
}
