package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/internal/telemetry"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestFailedAndSimulatedTxDoNotCountAsCommitted(t *testing.T) {
	alice := newTrader()
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 50)},
	}, nil)
	orders := telemetry.OrdersProcessed()
	trades := telemetry.TradesExecuted()
	deliver(t, application, alice.priv, place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 10, 1), false)
	require.Equal(t, orders, telemetry.OrdersProcessed())
	require.Equal(t, trades, telemetry.TradesExecuted())

	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 10),
	}, true)
	require.Equal(t, orders, telemetry.OrdersProcessed())

	acc := application.AccountKeeper.GetAccount(application.NewContext(true), alice.addr)
	msg := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 10, 1)
	bz := signedTx(t, application, alice, []sdk.Msg{msg}, sdk.NewCoins(sdk.NewInt64Coin("stake", 0)), 200_000, acc.GetAccountNumber(), acc.GetSequence())
	_, _, err := application.Simulate(bz)
	require.NoError(t, err)
	require.Equal(t, orders, telemetry.OrdersProcessed())

	deliver(t, application, alice.priv, msg, true)
	require.Equal(t, orders+1, telemetry.OrdersProcessed())
	require.Equal(t, trades, telemetry.TradesExecuted())
}
