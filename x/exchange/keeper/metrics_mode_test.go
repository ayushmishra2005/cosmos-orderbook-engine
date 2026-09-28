package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/internal/telemetry"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestCheckAndSimulateDoNotCountCommittedOrders(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	owner := addr(4)
	fund(t, k, ctx, owner, baseAsset, 10)
	msg := &v1.MsgPlaceOrder{
		Owner: sdk.AccAddress(owner).String(), MarketId: uint64(marketID),
		Side: v1.Side_SIDE_SELL, OrderType: v1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: v1.TimeInForce_TIME_IN_FORCE_GTC, QuantityLots: 1, PriceTicks: 10, CommandNonce: 1,
	}
	srv := NewMsgServer(k)
	orders := telemetry.OrdersProcessed()
	for _, mode := range []sdk.ExecMode{sdk.ExecModeCheck, sdk.ExecModeSimulate} {
		cache, _ := ctx.CacheContext()
		if _, err := srv.PlaceOrder(cache.WithExecMode(mode), msg); err != nil {
			t.Fatal(err)
		}
	}
	if telemetry.OrdersProcessed() != orders {
		t.Fatal("uncommitted execution counted a place")
	}
	if _, err := srv.PlaceOrder(ctx.WithExecMode(sdk.ExecModeFinalize), msg); err != nil {
		t.Fatal(err)
	}
	if telemetry.OrdersProcessed() != orders+1 {
		t.Fatalf("committed place %v", telemetry.OrdersProcessed())
	}
	if got := bal(t, k, ctx, owner, baseAsset); got.Locked != 1 {
		t.Fatalf("%+v", got)
	}
}
