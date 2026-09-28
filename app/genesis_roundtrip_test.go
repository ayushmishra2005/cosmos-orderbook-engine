package app

import (
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestGenesisRoundTrip(t *testing.T) {
	alice := replayTrader(40)
	bob := replayTrader(41)
	carol := replayTrader(42)
	sub := replayTrader(43)
	application := startReplayApp(t, []funded{
		{sub, coins("stake", 1_000_000_000_000)},
		{alice, coins("stake", 1_000_000_000_000, "base", 20)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 10_000)},
		{carol, coins("stake", 1_000_000_000_000, "quote", 1_000)},
	}, sub.addr)

	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 20),
	}, true)
	deliver(t, application, bob.priv, &exchangev1.MsgDeposit{
		Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 10_000),
	}, true)
	deliver(t, application, carol.priv, &exchangev1.MsgDeposit{
		Owner: carol.addr.String(), Amount: sdk.NewInt64Coin("quote", 1_000),
	}, true)

	aliceSell := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 10, 20, 1)
	aliceSell.ClientOrderId = []byte("desk-a")
	deliver(t, application, alice.priv, aliceSell, true)
	deliver(t, application, bob.priv, place(bob.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 4, 20, 1), true)
	carolBid := place(carol.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 5, 10, 1)
	carolBid.ClientOrderId = []byte("carol-bid")
	deliver(t, application, carol.priv, carolBid, true)

	expiry := uint64(application.LastBlockHeight()) + 30
	gtd := place(alice.addr, 2, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTD, 2, 7, 2)
	gtd.ExpiryHeight = expiry
	deliver(t, application, alice.priv, gtd, true)

	ctx := application.NewContext(true)
	rev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 1, ExpectedExchangeRevision: rev,
		PreviousBatchCommitment: make([]byte, 32),
		Commands:                []*batchv1.SignedCommand{replayPlace(t, bob, 2, 1, exchangev1.Side_SIDE_BUY, 1, 9)},
	}, true)

	ctx = application.NewContext(true)
	sellID := replayOrderID(t, alice.addr, 1, 1)
	gtdID := replayOrderID(t, alice.addr, 2, 2)
	carolID := replayOrderID(t, carol.addr, 1, 1)
	sell, err := application.Keeper.GetOrder(ctx, sellID)
	require.NoError(t, err)
	require.Equal(t, domain.Quantity(6), sell.Order.RemainingQuantity)
	storedGTD, err := application.Keeper.GetOrder(ctx, gtdID)
	require.NoError(t, err)
	require.Equal(t, expiry, storedGTD.Order.ExpiryHeight)
	trade, err := application.Keeper.GetTrade(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, domain.Quantity(4), trade.Quantity)
	require.Equal(t, exchangetypes.Balance{Available: 8, Locked: 8}, mustBal(t, application, alice.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 9_911, Locked: 9}, mustBal(t, application, bob.addr, 2))
	require.Equal(t, exchangetypes.Balance{Available: 950, Locked: 50}, mustBal(t, application, carol.addr, 2))
	require.Equal(t, uint64(2), mustNonce(t, application, alice.addr))
	require.Equal(t, uint64(2), mustNonce(t, application, bob.addr))
	require.Equal(t, uint64(1), mustNonce(t, application, carol.addr))
	orderSeq, err := application.Keeper.GetOrderSequence(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(3), orderSeq)
	tradeSeq, err := application.Keeper.GetTradeSequence(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), tradeSeq)
	m2Seq, err := application.Keeper.GetOrderSequence(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), m2Seq)
	wantRev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	head, err := application.BatchKeeper.LatestBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), head.Number)
	preHead := head.Commitment

	wantEx, err := application.Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	wantBatch, err := application.BatchKeeper.SnapshotDigest(ctx)
	require.NoError(t, err)

	restored := restartFromExport(t, application)
	ctx = restored.NewContext(true)
	gotEx, err := restored.Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	gotBatch, err := restored.BatchKeeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	require.Equal(t, wantEx, gotEx)
	require.Equal(t, wantBatch, gotBatch)
	require.NoError(t, restored.Keeper.CheckInvariants(ctx))
	require.NoError(t, restored.Keeper.CheckCustody(ctx))
	require.NoError(t, restored.BatchKeeper.CheckInvariants(ctx))
	importedHead, err := restored.BatchKeeper.LatestBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, preHead, importedHead.Commitment)
	importedRev, err := restored.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, wantRev, importedRev)
	importedGTD, err := restored.Keeper.GetOrder(ctx, gtdID)
	require.NoError(t, err)
	require.Equal(t, expiry, importedGTD.Order.ExpiryHeight)
	require.Equal(t, sell.MakerGross, mustOrder(t, restored, sellID).MakerGross)
	require.Equal(t, sell.TakerGross, mustOrder(t, restored, sellID).TakerGross)

	dup := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 30, 3)
	dup.ClientOrderId = []byte("desk-a")
	deliver(t, restored, alice.priv, dup, false)
	require.Equal(t, uint64(2), mustNonce(t, restored, alice.addr))
	require.NoError(t, restored.Keeper.CheckInvariants(restored.NewContext(true)))

	deliver(t, restored, carol.priv, &exchangev1.MsgCancelOrder{
		Owner: carol.addr.String(), OrderId: carolID[:], CommandNonce: 2,
	}, true)
	_, err = restored.Keeper.GetOrder(restored.NewContext(true), carolID)
	require.ErrorIs(t, err, exchangetypes.ErrNotFound)
	require.Equal(t, exchangetypes.Balance{Available: 1_000}, mustBal(t, restored, carol.addr, 2))

	deliver(t, restored, bob.priv, place(bob.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 6, 20, 3), true)
	_, err = restored.Keeper.GetOrder(restored.NewContext(true), sellID)
	require.ErrorIs(t, err, exchangetypes.ErrNotFound)
	filled, err := restored.Keeper.GetTrade(restored.NewContext(true), 1, 2)
	require.NoError(t, err)
	require.Equal(t, domain.Quantity(6), filled.Quantity)
	require.Equal(t, sellID, filled.MakerOrderID)

	ctx = restored.NewContext(true)
	rev, err = restored.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	deliver(t, restored, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 2, ExpectedExchangeRevision: rev,
		PreviousBatchCommitment: append([]byte(nil), preHead[:]...),
		Commands:                []*batchv1.SignedCommand{replayPlace(t, alice, 3, 1, exchangev1.Side_SIDE_SELL, 1, 30)},
	}, true)
	ctx = restored.NewContext(true)
	next, err := restored.BatchKeeper.LatestBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), next.Number)
	require.Equal(t, preHead, next.Previous)
	require.NotEqual(t, preHead, next.Commitment)
	require.NoError(t, restored.Keeper.CheckInvariants(ctx))
	require.NoError(t, restored.Keeper.CheckCustody(ctx))
	require.NoError(t, restored.BatchKeeper.CheckInvariants(ctx))
	liveGTD, err := restored.Keeper.GetOrder(ctx, gtdID)
	require.NoError(t, err)
	require.Equal(t, expiry, liveGTD.Order.ExpiryHeight)
}

func TestGenesisPartialFillFeeContinuity(t *testing.T) {
	require.Equal(t, feePath(t, false), feePath(t, true))
}

func TestGenesisGTDExpiryContinuity(t *testing.T) {
	alice := replayTrader(50)
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 4)},
	}, nil)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 4),
	}, true)
	expiry := uint64(application.LastBlockHeight()) + 3
	msg := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTD, 4, 5, 1)
	msg.ExpiryHeight = expiry
	deliver(t, application, alice.priv, msg, true)
	id := replayOrderID(t, alice.addr, 1, 1)
	stored, err := application.Keeper.GetOrder(application.NewContext(true), id)
	require.NoError(t, err)
	require.Equal(t, expiry, stored.Order.ExpiryHeight)

	restored := restartFromExport(t, application)
	imported, err := restored.Keeper.GetOrder(restored.NewContext(true), id)
	require.NoError(t, err)
	require.Equal(t, expiry, imported.Order.ExpiryHeight)
	require.Equal(t, exchangetypes.Balance{Locked: 4}, mustBal(t, restored, alice.addr, 1))

	rev, err := restored.Keeper.GetRevision(restored.NewContext(true))
	require.NoError(t, err)
	advanceTo(t, restored, int64(expiry)-1)
	still, err := restored.Keeper.GetOrder(restored.NewContext(true), id)
	require.NoError(t, err)
	require.Equal(t, expiry, still.Order.ExpiryHeight)
	require.Equal(t, exchangetypes.Balance{Locked: 4}, mustBal(t, restored, alice.addr, 1))
	sameRev, err := restored.Keeper.GetRevision(restored.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, rev, sameRev)

	advanceTo(t, restored, int64(expiry))
	_, err = restored.Keeper.GetOrder(restored.NewContext(true), id)
	require.ErrorIs(t, err, exchangetypes.ErrNotFound)
	require.Equal(t, exchangetypes.Balance{Available: 4}, mustBal(t, restored, alice.addr, 1))
	after, err := restored.Keeper.GetRevision(restored.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, rev+1, after)
	require.NoError(t, restored.Keeper.CheckInvariants(restored.NewContext(true)))

	advanceTo(t, restored, int64(expiry)+1)
	require.Equal(t, exchangetypes.Balance{Available: 4}, mustBal(t, restored, alice.addr, 1))
	again, err := restored.Keeper.GetRevision(restored.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, after, again)
}

func TestGenesisClientOrderIDContinuity(t *testing.T) {
	alice := replayTrader(51)
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 10)},
	}, nil)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 10),
	}, true)
	msg := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 2, 4, 1)
	msg.ClientOrderId = []byte("desk-1")
	deliver(t, application, alice.priv, msg, true)
	id := replayOrderID(t, alice.addr, 1, 1)

	restored := restartFromExport(t, application)
	dup := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 5, 2)
	dup.ClientOrderId = []byte("desk-1")
	deliver(t, restored, alice.priv, dup, false)
	require.Equal(t, uint64(1), mustNonce(t, restored, alice.addr))
	_, err := restored.Keeper.GetOrder(restored.NewContext(true), id)
	require.NoError(t, err)

	deliver(t, restored, alice.priv, &exchangev1.MsgCancelOrder{
		Owner: alice.addr.String(), OrderId: id[:], CommandNonce: 2,
	}, true)
	reuse := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 6, 3)
	reuse.ClientOrderId = []byte("desk-1")
	deliver(t, restored, alice.priv, reuse, true)
	again := place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 7, 4)
	again.ClientOrderId = []byte("desk-1")
	deliver(t, restored, alice.priv, again, false)
	require.NoError(t, restored.Keeper.CheckInvariants(restored.NewContext(true)))
}

func TestCorruptGenesisPanicsBeforeUse(t *testing.T) {
	alice := newTrader()
	require.Panics(t, func() {
		_, _ = buildApp(t, []funded{
			{alice, coins("stake", 1_000_000_000_000, "base", 6)},
		}, func(gs *exchangev1.GenesisState) {
			gs.Balances = []*exchangev1.GenesisBalance{{
				Owner: alice.addr.String(), AssetId: 1, Available: 1, Locked: 5,
			}}
		}, banktypes.Balance{
			Address: exchangekeeper.ModuleAddress().String(),
			Coins:   sdk.NewCoins(sdk.NewInt64Coin("base", 6)),
		})
	})
}

type feeSnap struct {
	TakerGross   uint64
	MakerGross   uint64
	LastMakerFee uint64
	Collector    uint64
}

func feePath(t *testing.T, interrupt bool) feeSnap {
	t.Helper()
	seller1 := replayTrader(31)
	seller2 := replayTrader(32)
	seller3 := replayTrader(33)
	buyer := replayTrader(34)
	application := startApp(t, []funded{
		{seller1, coins("stake", 1_000_000_000_000, "base", 1)},
		{seller2, coins("stake", 1_000_000_000_000, "base", 1)},
		{seller3, coins("stake", 1_000_000_000_000, "base", 1)},
		{buyer, coins("stake", 1_000_000_000_000, "quote", 10)},
	}, func(gs *exchangev1.GenesisState) {
		gs.Markets[0].MakerFeePpm = 1
		gs.Markets[0].TakerFeePpm = 1
	})
	deliver(t, application, seller1.priv, &exchangev1.MsgDeposit{Owner: seller1.addr.String(), Amount: sdk.NewInt64Coin("base", 1)}, true)
	deliver(t, application, seller2.priv, &exchangev1.MsgDeposit{Owner: seller2.addr.String(), Amount: sdk.NewInt64Coin("base", 1)}, true)
	deliver(t, application, seller3.priv, &exchangev1.MsgDeposit{Owner: seller3.addr.String(), Amount: sdk.NewInt64Coin("base", 1)}, true)
	deliver(t, application, buyer.priv, &exchangev1.MsgDeposit{Owner: buyer.addr.String(), Amount: sdk.NewInt64Coin("quote", 10)}, true)
	deliver(t, application, seller1.priv, place(seller1.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 1, 1), true)
	deliver(t, application, buyer.priv, place(buyer.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 3, 1, 1), true)
	deliver(t, application, seller2.priv, place(seller2.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 1, 1), true)
	if interrupt {
		application = restartFromExport(t, application)
	}
	id := replayOrderID(t, buyer.addr, 1, 1)
	resting, err := application.Keeper.GetOrder(application.NewContext(true), id)
	require.NoError(t, err)
	deliver(t, application, seller3.priv, place(seller3.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 1, 1), true)
	trade, err := application.Keeper.GetTrade(application.NewContext(true), 1, 3)
	require.NoError(t, err)
	return feeSnap{
		TakerGross:   resting.TakerGross,
		MakerGross:   resting.MakerGross,
		LastMakerFee: trade.MakerFee,
		Collector:    mustBal(t, application, exchangetypes.FeeCollectorOwner, 1).Available,
	}
}

func mustOrder(t *testing.T, application *App, id domain.OrderID) exchangetypes.StoredOrder {
	t.Helper()
	order, err := application.Keeper.GetOrder(application.NewContext(true), id)
	require.NoError(t, err)
	return order
}

func restartFromExport(t *testing.T, application *App) *App {
	t.Helper()
	exported, err := application.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	restored, err := New(log.NewNopLogger(), dbm.NewMemDB(), testChainID)
	require.NoError(t, err)
	_, err = restored.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes:   exported.AppState,
		Time:            time.Unix(1_700_000_000, 0).UTC(),
		InitialHeight:   exported.Height,
	})
	require.NoError(t, err)
	_, err = restored.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: exported.Height,
		Time:   time.Unix(1_700_000_000, 0).UTC(),
	})
	require.NoError(t, err)
	_, err = restored.Commit()
	require.NoError(t, err)
	return restored
}

func advanceTo(t *testing.T, application *App, height int64) {
	t.Helper()
	for application.LastBlockHeight() < height {
		next := application.LastBlockHeight() + 1
		_, err := application.FinalizeBlock(&abci.RequestFinalizeBlock{
			Height: next,
			Time:   time.Unix(1_700_000_000, 0).Add(time.Duration(next) * time.Second).UTC(),
		})
		require.NoError(t, err)
		_, err = application.Commit()
		require.NoError(t, err)
	}
}
