package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestExportContinuationMatchesUninterrupted(t *testing.T) {
	alice := newTrader()
	bob := newTrader()
	live := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 100)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 1000)},
	}, nil)
	deliver(t, live, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 20),
	}, true)
	deliver(t, live, bob.priv, &exchangev1.MsgDeposit{
		Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 200),
	}, true)
	deliver(t, live, alice.priv, place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 5, 10, 1), true)

	committed := live.LastBlockHeight()
	require.Greater(t, committed, int64(1))
	_, err := live.ExportAppStateAndValidators(true, nil, nil)
	require.ErrorContains(t, err, "zero-height export is not supported")
	require.Equal(t, committed, live.LastBlockHeight())

	exported, err := live.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	require.Equal(t, committed+1, exported.Height)

	restored := restartFromExport(t, live)
	require.Equal(t, int64(0), restored.LastBlockHeight())
	require.Equal(t, exported.Height, nextExecutionHeight(restored))

	next := place(bob.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 5, 10, 1)
	deliver(t, live, bob.priv, next, true)
	deliver(t, restored, bob.priv, next, true)

	require.Equal(t, committed+1, live.LastBlockHeight())
	require.Equal(t, committed+1, restored.LastBlockHeight())
	requireSameContinuation(t, live, restored)
}

func requireSameContinuation(t *testing.T, live, restored *App) {
	t.Helper()
	ctxL := qctx(live)
	ctxR := qctx(restored)
	revL, err := live.Keeper.GetRevision(ctxL)
	require.NoError(t, err)
	revR, err := restored.Keeper.GetRevision(ctxR)
	require.NoError(t, err)
	require.Equal(t, revL, revR)
	for _, market := range []domain.MarketID{1} {
		ol, err := live.Keeper.GetOrderSequence(ctxL, market)
		require.NoError(t, err)
		or, err := restored.Keeper.GetOrderSequence(ctxR, market)
		require.NoError(t, err)
		require.Equal(t, ol, or)
		tl, err := live.Keeper.GetTradeSequence(ctxL, market)
		require.NoError(t, err)
		tr, err := restored.Keeper.GetTradeSequence(ctxR, market)
		require.NoError(t, err)
		require.Equal(t, tl, tr)
		require.Equal(t, uint64(1), tr)
	}
	headL, err := live.BatchKeeper.GetParams(ctxL)
	require.NoError(t, err)
	headR, err := restored.BatchKeeper.GetParams(ctxR)
	require.NoError(t, err)
	require.Equal(t, headL.Latest, headR.Latest)
	require.Equal(t, headL.Head, headR.Head)
	exL, err := live.Keeper.SnapshotDigest(ctxL)
	require.NoError(t, err)
	exR, err := restored.Keeper.SnapshotDigest(ctxR)
	require.NoError(t, err)
	require.Equal(t, exL, exR)
	batchL, err := live.BatchKeeper.SnapshotDigest(ctxL)
	require.NoError(t, err)
	batchR, err := restored.BatchKeeper.SnapshotDigest(ctxR)
	require.NoError(t, err)
	require.Equal(t, batchL, batchR)
	require.True(t, headL.Head.IsZero())
}
