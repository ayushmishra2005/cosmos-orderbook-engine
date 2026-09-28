package app

import (
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestFourValidatorsAgree(t *testing.T) {
	const n = 4
	maker := replayTrader(1)
	taker := replayTrader(2)
	sub := replayTrader(3)
	apps := make([]*App, n)
	for i := 0; i < n; i++ {
		apps[i] = startReplayApp(t, []funded{
			{sub, coins("stake", 1_000_000_000_000)},
			{maker, coins("stake", 1_000_000_000_000, "base", 1_000, "quote", 1_000)},
			{taker, coins("stake", 1_000_000_000_000, "quote", 10_000)},
		}, sub.addr)
	}
	for _, application := range apps {
		runAgreement(t, application, maker, taker, sub)
	}
	ctx := apps[0].NewContext(true)
	wantEx, err := apps[0].Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	wantBatch, err := apps[0].BatchKeeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	wantRev, err := apps[0].Keeper.GetRevision(ctx)
	require.NoError(t, err)
	wantTrade, err := apps[0].Keeper.GetTradeSequence(ctx, 1)
	require.NoError(t, err)
	head, err := apps[0].BatchKeeper.LatestBatch(ctx)
	require.NoError(t, err)
	for i := 1; i < n; i++ {
		ctx := apps[i].NewContext(true)
		gotEx, err := apps[i].Keeper.SnapshotDigest(ctx)
		require.NoError(t, err)
		gotBatch, err := apps[i].BatchKeeper.SnapshotDigest(ctx)
		require.NoError(t, err)
		require.Equal(t, wantEx, gotEx)
		require.Equal(t, wantBatch, gotBatch)
		rev, err := apps[i].Keeper.GetRevision(ctx)
		require.NoError(t, err)
		require.Equal(t, wantRev, rev)
		trade, err := apps[i].Keeper.GetTradeSequence(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, wantTrade, trade)
		got, err := apps[i].BatchKeeper.LatestBatch(ctx)
		require.NoError(t, err)
		require.Equal(t, head.Number, got.Number)
		require.Equal(t, head.ID, got.ID)
		require.Equal(t, head.ResultsHash, got.ResultsHash)
		require.Equal(t, head.Commitment, got.Commitment)
		require.NoError(t, apps[i].Keeper.CheckInvariants(ctx))
		require.NoError(t, apps[i].Keeper.CheckCustody(ctx))
		require.NoError(t, apps[i].BatchKeeper.CheckInvariants(ctx))
	}
}

func runAgreement(t *testing.T, application *App, maker, taker, sub trader) {
	t.Helper()
	deliver(t, application, maker.priv, &exchangev1.MsgDeposit{
		Owner: maker.addr.String(), Amount: sdk.NewInt64Coin("base", 100),
	}, true)
	deliver(t, application, taker.priv, &exchangev1.MsgDeposit{
		Owner: taker.addr.String(), Amount: sdk.NewInt64Coin("quote", 1_000),
	}, true)
	deliver(t, application, maker.priv, place(maker.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 10, 8, 1), true)
	deliver(t, application, taker.priv, place(taker.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_IOC, 4, 8, 1), true)
	ctx := application.NewContext(true)
	rev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	cmd := replayPlace(t, taker, 2, 1, exchangev1.Side_SIDE_BUY, 2, 8)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter:                sub.addr.String(),
		BatchNumber:              1,
		ExpectedExchangeRevision: rev,
		PreviousBatchCommitment:  make([]byte, 32),
		Commands:                 []*batchv1.SignedCommand{cmd},
	}, true)
}

func TestReplaySnapshotThreeTimes(t *testing.T) {
	var first [32]byte
	var firstBatch [32]byte
	for i := 0; i < 3; i++ {
		application := runRichReplay(t)
		ctx := application.NewContext(true)
		ex, err := application.Keeper.SnapshotDigest(ctx)
		require.NoError(t, err)
		batch, err := application.BatchKeeper.SnapshotDigest(ctx)
		require.NoError(t, err)
		require.NoError(t, application.Keeper.CheckInvariants(ctx))
		require.NoError(t, application.Keeper.CheckCustody(ctx))
		require.NoError(t, application.BatchKeeper.CheckInvariants(ctx))
		if i == 0 {
			first, firstBatch = ex, batch
			continue
		}
		require.Equal(t, first, ex)
		require.Equal(t, firstBatch, batch)
	}
}

func runRichReplay(t *testing.T) *App {
	t.Helper()
	maker := replayTrader(4)
	taker := replayTrader(5)
	sub := replayTrader(6)
	application := startReplayApp(t, []funded{
		{sub, coins("stake", 1_000_000_000_000)},
		{maker, coins("stake", 1_000_000_000_000, "base", 1_000, "quote", 1_000)},
		{taker, coins("stake", 1_000_000_000_000, "base", 100, "quote", 10_000)},
	}, sub.addr)
	deliver(t, application, maker.priv, &exchangev1.MsgDeposit{
		Owner: maker.addr.String(), Amount: sdk.NewInt64Coin("base", 100),
	}, true)
	deliver(t, application, maker.priv, &exchangev1.MsgDeposit{
		Owner: maker.addr.String(), Amount: sdk.NewInt64Coin("quote", 100),
	}, true)
	deliver(t, application, taker.priv, &exchangev1.MsgDeposit{
		Owner: taker.addr.String(), Amount: sdk.NewInt64Coin("quote", 1_000),
	}, true)
	deliver(t, application, taker.priv, &exchangev1.MsgDeposit{
		Owner: taker.addr.String(), Amount: sdk.NewInt64Coin("base", 20),
	}, true)
	deliver(t, application, maker.priv, place(maker.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 10, 10, 1), true)
	sellID := replayOrderID(t, maker.addr, 1, 1)
	stored, err := application.Keeper.GetOrder(application.NewContext(true), sellID)
	require.NoError(t, err)
	seq := stored.Order.Sequence
	deliver(t, application, taker.priv, place(taker.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 4, 10, 1), true)
	stored, err = application.Keeper.GetOrder(application.NewContext(true), sellID)
	require.NoError(t, err)
	require.Equal(t, seq, stored.Order.Sequence)
	require.Equal(t, domain.Quantity(6), stored.Order.RemainingQuantity)

	ctx := application.NewContext(true)
	rev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	ioc, _ := replaySign(t, taker, canonical.Command{
		ProtocolVersion: canonical.BatchCommandVersion, ChainID: testChainID,
		ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID), Owner: taker.addr, Nonce: 2,
		Type: canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID: 1, Side: domain.SideBuy, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceIOC,
			Quantity: 2, Price: 10,
		},
	})
	ioc.Place.TimeInForce = batchv1.TimeInForce_TIME_IN_FORCE_IOC
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 1, ExpectedExchangeRevision: rev,
		PreviousBatchCommitment: make([]byte, 32), Commands: []*batchv1.SignedCommand{ioc},
	}, true)
	_, err = application.Keeper.GetOrder(application.NewContext(true), orderID(t, taker.addr, 2))
	require.Error(t, err)

	expiry := uint64(application.LastBlockHeight()) + 2
	gtd := place(maker.addr, 2, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTD, 1, 4, 2)
	gtd.ExpiryHeight = expiry
	deliver(t, application, maker.priv, gtd, true)
	gtdID := replayOrderID(t, maker.addr, 2, 2)
	_, err = application.Keeper.GetOrder(application.NewContext(true), gtdID)
	require.NoError(t, err)
	deliver(t, application, taker.priv, &exchangev1.MsgWithdraw{
		Owner: taker.addr.String(), Amount: sdk.NewInt64Coin("base", 1),
	}, true)
	_, err = application.Keeper.GetOrder(application.NewContext(true), gtdID)
	require.Error(t, err)

	before, err := application.Keeper.SnapshotDigest(application.NewContext(true))
	require.NoError(t, err)
	deliver(t, application, taker.priv, place(taker.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_FOK, 100, 10, 3), false)
	after, err := application.Keeper.SnapshotDigest(application.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, before, after)
	return application
}

func orderID(t *testing.T, owner sdk.AccAddress, nonce uint64) domain.OrderID {
	t.Helper()
	return replayOrderID(t, owner, 1, nonce)
}

func TestExportImportAvailableBalances(t *testing.T) {
	alice := replayTrader(7)
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 500, "quote", 500)},
	}, nil)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 40),
	}, true)
	deliver(t, application, alice.priv, &exchangev1.MsgWithdraw{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 15),
	}, true)
	ctx := application.NewContext(true)
	want, err := application.Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
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
	got, err := restored.Keeper.SnapshotDigest(restored.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoError(t, restored.Keeper.CheckInvariants(restored.NewContext(true)))
	require.NoError(t, restored.Keeper.CheckCustody(restored.NewContext(true)))
}

func TestExportOpenBookDoesNotValidate(t *testing.T) {
	alice := replayTrader(8)
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 50)},
	}, nil)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 10),
	}, true)
	deliver(t, application, alice.priv, place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 4, 3, 1), true)
	gs, err := application.Keeper.ExportGenesis(application.NewContext(true))
	require.NoError(t, err)
	require.Error(t, gs.Validate())
}

func TestRestartFromDatabase(t *testing.T) {
	db := dbm.NewMemDB()
	alice := replayTrader(11)
	bob := replayTrader(12)
	sub := replayTrader(13)
	application := bootDB(t, db, []funded{
		{sub, coins("stake", 1_000_000_000_000)},
		{alice, coins("stake", 1_000_000_000_000, "base", 100)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 1_000)},
	}, sub.addr)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 20),
	}, true)
	deliver(t, application, bob.priv, &exchangev1.MsgDeposit{
		Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 200),
	}, true)
	deliver(t, application, alice.priv, place(alice.addr, 1, exchangev1.Side_SIDE_SELL, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 5, 4, 1), true)
	deliver(t, application, bob.priv, place(bob.addr, 1, exchangev1.Side_SIDE_BUY, exchangev1.OrderType_ORDER_TYPE_LIMIT, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 2, 4, 1), true)
	ctx := application.NewContext(true)
	wantEx, err := application.Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	wantBatch, err := application.BatchKeeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	wantRev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)

	restored, err := New(log.NewNopLogger(), db, testChainID)
	require.NoError(t, err)
	ctx = restored.NewContext(true)
	gotEx, err := restored.Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	gotBatch, err := restored.BatchKeeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	gotRev, err := restored.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, wantEx, gotEx)
	require.Equal(t, wantBatch, gotBatch)
	require.Equal(t, wantRev, gotRev)
	require.NoError(t, restored.Keeper.CheckInvariants(ctx))
}

func TestBankFailuresStayAtomic(t *testing.T) {
	alice := replayTrader(14)
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 30, "quote", 30)},
	}, nil)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 12),
	}, true)
	ctx := application.NewContext(true)
	user := application.BankKeeper.GetBalance(ctx, alice.addr, "base")
	module := application.BankKeeper.GetBalance(ctx, exchangekeeper.ModuleAddress(), "base")
	internal := mustBal(t, application, alice.addr, 1)

	application.Keeper.SetFailStageForTest(exchangekeeper.FailAfterBankDeposit)
	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 5),
	}, false)
	application.Keeper.SetFailStageForTest("")
	ctx = application.NewContext(true)
	require.True(t, user.Equal(application.BankKeeper.GetBalance(ctx, alice.addr, "base")))
	require.True(t, module.Equal(application.BankKeeper.GetBalance(ctx, exchangekeeper.ModuleAddress(), "base")))
	require.Equal(t, internal, mustBal(t, application, alice.addr, 1))

	application.Keeper.SetFailStageForTest(exchangekeeper.FailBeforeBankWithdraw)
	deliver(t, application, alice.priv, &exchangev1.MsgWithdraw{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 4),
	}, false)
	application.Keeper.SetFailStageForTest("")
	ctx = application.NewContext(true)
	require.True(t, user.Equal(application.BankKeeper.GetBalance(ctx, alice.addr, "base")))
	require.True(t, module.Equal(application.BankKeeper.GetBalance(ctx, exchangekeeper.ModuleAddress(), "base")))
	require.Equal(t, internal, mustBal(t, application, alice.addr, 1))

	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 100),
	}, false)
	deliver(t, application, alice.priv, &exchangev1.MsgWithdraw{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 13),
	}, false)
	ctx = application.NewContext(true)
	require.True(t, user.Equal(application.BankKeeper.GetBalance(ctx, alice.addr, "base")))
	require.Equal(t, internal, mustBal(t, application, alice.addr, 1))
	require.NoError(t, application.Keeper.CheckCustody(ctx))
}

func bootDB(t *testing.T, db dbm.DB, accounts []funded, submitter sdk.AccAddress) *App {
	t.Helper()
	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	genAccs := make([]authtypes.GenesisAccount, 0, len(accounts))
	balances := make([]banktypes.Balance, 0, len(accounts))
	for _, acc := range accounts {
		genAccs = append(genAccs, authtypes.NewBaseAccount(acc.tr.addr, acc.tr.priv.PubKey(), 0, 0))
		balances = append(balances, banktypes.Balance{Address: acc.tr.addr.String(), Coins: acc.coins})
	}
	application, err := New(log.NewNopLogger(), db, testChainID)
	require.NoError(t, err)
	genesis := application.DefaultGenesis()
	var batchGS batchv1.GenesisState
	require.NoError(t, application.Codec().UnmarshalJSON(genesis[batchtypes.ModuleName], &batchGS))
	batchGS.Submitter = submitter.String()
	genesis[batchtypes.ModuleName] = application.Codec().MustMarshalJSON(&batchGS)
	genesis, err = simtestutil.GenesisStateWithValSet(application.Codec(), genesis, valSet, genAccs, balances...)
	require.NoError(t, err)
	state, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = application.InitChain(&abci.RequestInitChain{
		ChainId: testChainID, ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes: state, Time: time.Unix(1_700_000_000, 0).UTC(), InitialHeight: 1,
	})
	require.NoError(t, err)
	_, err = application.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: 1, Time: time.Unix(1_700_000_000, 0).UTC(), NextValidatorsHash: valSet.Hash(),
	})
	require.NoError(t, err)
	_, err = application.Commit()
	require.NoError(t, err)
	return application
}

func TestQueryPageLimit(t *testing.T) {
	alice := newTrader()
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 10)},
	}, nil)
	qs := exchangekeeper.NewQueryServer(application.Keeper)
	_, err := qs.Markets(application.NewContext(true), &exchangev1.QueryMarketsRequest{Limit: 101})
	require.Error(t, err)
}
