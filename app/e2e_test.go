package app

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"unsafe"

	storestate "github.com/cosmos/cosmos-sdk/baseapp/state"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

const testChainID = "orderbook-test"

type trader struct {
	priv cryptotypes.PrivKey
	addr sdk.AccAddress
}

func TestMatchDepositWithdraw(t *testing.T) {
	alice := newTrader()
	bob := newTrader()
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 1000)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 10_000)},
	}, func(gs *v1.GenesisState) {
		gs.Markets[0].MakerFeePpm = 1000
		gs.Markets[0].TakerFeePpm = 2000
	})

	deliver(t, application, alice.priv, &v1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 1000),
	}, true)
	deliver(t, application, bob.priv, &v1.MsgDeposit{
		Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 10_000),
	}, true)
	deliver(t, application, alice.priv, place(alice.addr, 1, v1.Side_SIDE_SELL, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 150, 10, 1), true)
	deliver(t, application, bob.priv, place(bob.addr, 1, v1.Side_SIDE_BUY, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 100, 10, 1), true)

	ctx := application.NewContext(true)
	aliceSell, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID: testChainID, ExchangeInstanceID: []byte(v1.DefaultInstanceID),
		Owner: alice.addr, MarketID: 1, CommandNonce: 1,
	})
	require.NoError(t, err)
	stored, err := application.Keeper.GetOrder(ctx, aliceSell)
	require.NoError(t, err)
	require.Equal(t, domain.Quantity(50), stored.Order.RemainingQuantity)
	require.Equal(t, domain.Sequence(1), stored.Order.Sequence)
	require.Equal(t, domain.SideSell, stored.Order.Side)

	trade, err := application.Keeper.GetTrade(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, domain.Price(10), trade.Price)
	require.Equal(t, domain.Quantity(100), trade.Quantity)
	require.Equal(t, uint64(1), trade.MakerFee)
	require.Equal(t, uint64(1), trade.TakerFee)

	require.Equal(t, exchangetypes.Balance{Available: 850, Locked: 50}, mustBal(t, application, alice.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 999}, mustBal(t, application, alice.addr, 2))
	require.Equal(t, exchangetypes.Balance{Available: 99}, mustBal(t, application, bob.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 9000}, mustBal(t, application, bob.addr, 2))
	require.Equal(t, exchangetypes.Balance{Available: 1}, mustBal(t, application, exchangetypes.FeeCollectorOwner, 1))
	require.Equal(t, exchangetypes.Balance{Available: 1}, mustBal(t, application, exchangetypes.FeeCollectorOwner, 2))
	rev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(4), rev)
	requireCustody(t, application)

	qs := exchangekeeper.NewQueryServer(application.Keeper)
	gotTrade, err := qs.Trades(ctx, &v1.QueryTradesRequest{MarketId: 1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, gotTrade.Trades, 1)
	orderRes, err := qs.Order(ctx, &v1.QueryOrderRequest{OrderId: aliceSell[:]})
	require.NoError(t, err)
	require.Equal(t, uint64(50), orderRes.Order.RemainingLots)

	deliver(t, application, alice.priv, &v1.MsgWithdraw{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("quote", 999)}, true)
	deliver(t, application, bob.priv, &v1.MsgWithdraw{Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("base", 99)}, true)
	deliver(t, application, bob.priv, &v1.MsgWithdraw{Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 9000)}, true)
	deliver(t, application, alice.priv, &v1.MsgWithdraw{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 851)}, false)
	deliver(t, application, alice.priv, &v1.MsgWithdraw{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 850)}, true)

	ctx = application.NewContext(true)
	require.Equal(t, sdkmath.NewInt(850), application.BankKeeper.GetBalance(ctx, alice.addr, "base").Amount)
	require.Equal(t, sdkmath.NewInt(999), application.BankKeeper.GetBalance(ctx, alice.addr, "quote").Amount)
	require.Equal(t, sdkmath.NewInt(99), application.BankKeeper.GetBalance(ctx, bob.addr, "base").Amount)
	require.Equal(t, sdkmath.NewInt(9000), application.BankKeeper.GetBalance(ctx, bob.addr, "quote").Amount)
	require.Equal(t, exchangetypes.Balance{Locked: 50}, mustBal(t, application, alice.addr, 1))
	requireCustody(t, application)
}

func TestQueriesAndPagination(t *testing.T) {
	alice := newTrader()
	bob := newTrader()
	application := startApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 1000, "quote", 1000)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 10_000)},
	}, func(gs *v1.GenesisState) {
		gs.Markets = append(gs.Markets, &v1.Market{
			Id: 2, BaseAssetId: 1, QuoteAssetId: 2,
			BaseLotSize: 1, QuoteAtomsPerTickPerLot: 1, MaxMakerVisits: 64, Enabled: true,
		})
	})

	deliver(t, application, alice.priv, &v1.MsgDeposit{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 1000)}, true)
	deliver(t, application, alice.priv, &v1.MsgDeposit{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("quote", 1000)}, true)
	deliver(t, application, bob.priv, &v1.MsgDeposit{Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 10_000)}, true)
	deliver(t, application, alice.priv, place(alice.addr, 1, v1.Side_SIDE_SELL, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 10, 10, 1), true)
	deliver(t, application, alice.priv, place(alice.addr, 1, v1.Side_SIDE_SELL, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 10, 12, 2), true)
	deliver(t, application, bob.priv, place(bob.addr, 1, v1.Side_SIDE_BUY, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 10, 9, 1), true)
	deliver(t, application, bob.priv, place(bob.addr, 1, v1.Side_SIDE_BUY, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 10, 8, 2), true)

	ctx := application.NewContext(true)
	qs := exchangekeeper.NewQueryServer(application.Keeper)

	market, err := qs.Market(ctx, &v1.QueryMarketRequest{MarketId: 1})
	require.NoError(t, err)
	require.Equal(t, "base", market.Market.BaseDenom)
	require.Equal(t, "quote", market.Market.QuoteDenom)
	require.True(t, market.Market.Enabled)

	page, err := qs.Markets(ctx, &v1.QueryMarketsRequest{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Markets, 1)
	require.Equal(t, uint64(1), page.NextOffset)
	page2, err := qs.Markets(ctx, &v1.QueryMarketsRequest{Limit: 1, Offset: page.NextOffset})
	require.NoError(t, err)
	require.Len(t, page2.Markets, 1)
	require.NotEqual(t, page.Markets[0].Id, page2.Markets[0].Id)

	bal, err := qs.Balance(ctx, &v1.QueryBalanceRequest{Owner: alice.addr.String(), AssetId: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(980), bal.Balance.Available)
	require.Equal(t, uint64(20), bal.Balance.Locked)

	bals, err := qs.Balances(ctx, &v1.QueryBalancesRequest{Owner: alice.addr.String(), Limit: 1})
	require.NoError(t, err)
	require.Len(t, bals.Balances, 1)
	require.Equal(t, uint64(1), bals.NextOffset)

	book, err := qs.Orderbook(ctx, &v1.QueryOrderbookRequest{MarketId: 1, Depth: 10})
	require.NoError(t, err)
	require.Len(t, book.Asks, 2)
	require.Len(t, book.Bids, 2)
	require.Less(t, book.Asks[0].PriceTicks, book.Asks[1].PriceTicks)
	require.Greater(t, book.Bids[0].PriceTicks, book.Bids[1].PriceTicks)

	open, err := qs.OpenOrders(ctx, &v1.QueryOpenOrdersRequest{Owner: alice.addr.String(), MarketId: 1, Limit: 1})
	require.NoError(t, err)
	require.Len(t, open.Orders, 1)
	open2, err := qs.OpenOrders(ctx, &v1.QueryOpenOrdersRequest{Owner: alice.addr.String(), MarketId: 1, Limit: 1, Offset: open.NextOffset})
	require.NoError(t, err)
	require.Len(t, open2.Orders, 1)
	require.NotEqual(t, open.Orders[0].OrderId, open2.Orders[0].OrderId)

	_, err = qs.Markets(ctx, &v1.QueryMarketsRequest{Limit: 101})
	require.ErrorIs(t, err, exchangetypes.ErrLimit)

	deliver(t, application, bob.priv, place(bob.addr, 1, v1.Side_SIDE_BUY, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 20, 12, 3), true)
	ctx = application.NewContext(true)
	first, err := qs.Trades(ctx, &v1.QueryTradesRequest{MarketId: 1, Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Trades, 1)
	require.Equal(t, uint64(1), first.Trades[0].Sequence)
	require.Equal(t, uint64(1), first.NextSequence)
	second, err := qs.Trades(ctx, &v1.QueryTradesRequest{MarketId: 1, Limit: 1, AfterSequence: first.NextSequence})
	require.NoError(t, err)
	require.Len(t, second.Trades, 1)
	require.Equal(t, uint64(2), second.Trades[0].Sequence)
	require.Greater(t, second.Trades[0].PriceTicks, first.Trades[0].PriceTicks)
}

func TestFailedCommandsAreAtomic(t *testing.T) {
	alice := newTrader()
	application := startApp(t, []funded{{alice, coins("stake", 1_000_000_000_000, "base", 10)}}, nil)

	beforeBank := application.BankKeeper.GetBalance(application.NewContext(true), alice.addr, "base").Amount
	deliver(t, application, alice.priv, &v1.MsgDeposit{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("nope", 1)}, false)
	require.True(t, beforeBank.Equal(application.BankKeeper.GetBalance(application.NewContext(true), alice.addr, "base").Amount))
	require.Equal(t, exchangetypes.Balance{}, mustBal(t, application, alice.addr, 1))

	deliver(t, application, alice.priv, &v1.MsgDeposit{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 10)}, true)
	rev, err := application.Keeper.GetRevision(application.NewContext(true))
	require.NoError(t, err)
	deliver(t, application, alice.priv, &v1.MsgWithdraw{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 11)}, false)
	require.Equal(t, exchangetypes.Balance{Available: 10}, mustBal(t, application, alice.addr, 1))
	gotRev, err := application.Keeper.GetRevision(application.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, rev, gotRev)
	require.True(t, sdkmath.NewInt(0).Equal(application.BankKeeper.GetBalance(application.NewContext(true), alice.addr, "base").Amount))

	deliver(t, application, alice.priv, place(alice.addr, 1, v1.Side_SIDE_SELL, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC, 11, 10, 1), false)
	require.Equal(t, uint64(0), mustNonce(t, application, alice.addr))
	require.Equal(t, exchangetypes.Balance{Available: 10}, mustBal(t, application, alice.addr, 1))

	var missing domain.OrderID
	missing[0] = 1
	deliver(t, application, alice.priv, &v1.MsgCancelOrder{
		Owner: alice.addr.String(), OrderId: missing[:], CommandNonce: 1,
	}, false)
	require.Equal(t, uint64(0), mustNonce(t, application, alice.addr))
	requireCustody(t, application)
}

func TestGenesisBacking(t *testing.T) {
	alice := newTrader()
	val := newTrader()
	require.Panics(t, func() {
		_, _ = buildApp(t, []funded{
			{val, coins("stake", 1_000_000_000_000)},
			{alice, coins("stake", 1_000_000_000_000, "base", 10)},
		}, func(gs *v1.GenesisState) {
			gs.Balances = []*v1.GenesisBalance{{Owner: alice.addr.String(), AssetId: 1, Available: 10}}
		})
	})

	application := startApp(t, []funded{{alice, coins("stake", 1_000_000_000_000)}}, func(gs *v1.GenesisState) {
		gs.Balances = []*v1.GenesisBalance{{Owner: alice.addr.String(), AssetId: 1, Available: 10}}
	}, banktypes.Balance{
		Address: exchangekeeper.ModuleAddress().String(),
		Coins:   sdk.NewCoins(sdk.NewInt64Coin("base", 10)),
	})
	require.Equal(t, exchangetypes.Balance{Available: 10}, mustBal(t, application, alice.addr, 1))
	requireCustody(t, application)
	deliver(t, application, alice.priv, &v1.MsgWithdraw{Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 10)}, true)
	require.Equal(t, sdkmath.NewInt(10), application.BankKeeper.GetBalance(application.NewContext(true), alice.addr, "base").Amount)
	requireCustody(t, application)
}

type funded struct {
	tr    trader
	coins sdk.Coins
}

func coins(pairs ...any) sdk.Coins {
	out := sdk.NewCoins()
	for i := 0; i < len(pairs); i += 2 {
		var amount int64
		switch n := pairs[i+1].(type) {
		case int:
			amount = int64(n)
		case int64:
			amount = n
		default:
			panic("coin amount must be an integer")
		}
		out = out.Add(sdk.NewInt64Coin(pairs[i].(string), amount))
	}
	return out
}

func startApp(t *testing.T, accounts []funded, edit func(*v1.GenesisState), extra ...banktypes.Balance) *App {
	t.Helper()
	application, err := buildApp(t, accounts, edit, extra...)
	require.NoError(t, err)
	return application
}

func buildApp(t *testing.T, accounts []funded, edit func(*v1.GenesisState), extra ...banktypes.Balance) (*App, error) {
	t.Helper()
	return buildAppFull(t, accounts, edit, nil, extra...)
}

func buildAppFull(t *testing.T, accounts []funded, edit func(*v1.GenesisState), batchEdit func(*batchv1.GenesisState), extra ...banktypes.Balance) (*App, error) {
	t.Helper()
	valSet, err := simtestutil.CreateRandomValidatorSet()
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		t.Fatal("need a validator account")
	}
	genAccs := make([]authtypes.GenesisAccount, 0, len(accounts))
	balances := make([]banktypes.Balance, 0, len(accounts)+len(extra))
	for _, acc := range accounts {
		genAccs = append(genAccs, authtypes.NewBaseAccount(acc.tr.addr, acc.tr.priv.PubKey(), 0, 0))
		balances = append(balances, banktypes.Balance{Address: acc.tr.addr.String(), Coins: acc.coins})
	}
	balances = append(balances, extra...)
	application, err := New(log.NewNopLogger(), dbm.NewMemDB(), testChainID)
	if err != nil {
		return nil, err
	}
	genesis := application.DefaultGenesis()
	if edit != nil {
		var gs v1.GenesisState
		if err := application.Codec().UnmarshalJSON(genesis[exchangetypes.ModuleName], &gs); err != nil {
			return nil, err
		}
		edit(&gs)
		genesis[exchangetypes.ModuleName] = application.Codec().MustMarshalJSON(&gs)
	}
	if batchEdit != nil {
		var gs batchv1.GenesisState
		if err := application.Codec().UnmarshalJSON(genesis[batchtypes.ModuleName], &gs); err != nil {
			return nil, err
		}
		batchEdit(&gs)
		genesis[batchtypes.ModuleName] = application.Codec().MustMarshalJSON(&gs)
	}
	genesis, err = simtestutil.GenesisStateWithValSet(application.Codec(), genesis, valSet, genAccs, balances...)
	if err != nil {
		return nil, err
	}
	state, err := json.Marshal(genesis)
	if err != nil {
		return nil, err
	}
	if _, err := application.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes:   state,
		Time:            time.Unix(1_700_000_000, 0).UTC(),
		InitialHeight:   1,
	}); err != nil {
		return nil, err
	}
	if _, err := application.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:             1,
		Time:               time.Unix(1_700_000_000, 0).UTC(),
		NextValidatorsHash: valSet.Hash(),
	}); err != nil {
		return nil, err
	}
	if _, err := application.Commit(); err != nil {
		return nil, err
	}
	return application, nil
}

func newTrader() trader {
	priv := secp256k1.GenPrivKey()
	return trader{priv: priv, addr: sdk.AccAddress(priv.PubKey().Address())}
}

func place(owner sdk.AccAddress, market uint64, side v1.Side, typ v1.OrderType, tif v1.TimeInForce, qty, price, nonce uint64) *v1.MsgPlaceOrder {
	return &v1.MsgPlaceOrder{
		Owner: owner.String(), MarketId: market, Side: side, OrderType: typ, TimeInForce: tif,
		QuantityLots: qty, PriceTicks: price, CommandNonce: nonce,
	}
}

func deliver(t *testing.T, application *App, priv cryptotypes.PrivKey, msg sdk.Msg, expPass bool) {
	t.Helper()
	addr := sdk.AccAddress(priv.PubKey().Address())
	acc := application.AccountKeeper.GetAccount(qctx(application), addr)
	require.NotNil(t, acc)
	height := nextExecutionHeight(application)
	if application.LastBlockHeight() == 0 {
		bz := signedTx(t, application, trader{priv: priv, addr: addr}, []sdk.Msg{msg}, sdk.NewCoins(sdk.NewInt64Coin(sdk.DefaultBondDenom, 0)), simtestutil.DefaultGenTxGas, acc.GetAccountNumber(), acc.GetSequence())
		res, err := application.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Txs: [][]byte{bz}})
		require.NoError(t, err)
		require.Len(t, res.TxResults, 1)
		if expPass {
			require.Zero(t, res.TxResults[0].Code, res.TxResults[0].Log)
		} else {
			require.NotZero(t, res.TxResults[0].Code)
		}
		_, err = application.Commit()
		require.NoError(t, err)
		return
	}
	header := cmtproto.Header{Height: height, Time: time.Unix(1_700_000_000, 0).UTC()}
	_, _, err := simtestutil.SignCheckDeliver(
		t, application.TxConfig(), application.BaseApp, header, []sdk.Msg{msg}, testChainID,
		[]uint64{acc.GetAccountNumber()}, []uint64{acc.GetSequence()}, expPass, expPass, priv,
	)
	if expPass {
		require.NoError(t, err)
	}
}

// qctx reads the imported genesis while InitChain's writes are still in the
// uncommitted finalize branch. After the first commit, it is the committed store.
func qctx(application *App) sdk.Context {
	if application.LastBlockHeight() > 0 {
		return application.NewContext(true)
	}
	field := reflect.ValueOf(application.BaseApp).Elem().FieldByName("stateManager")
	mgr := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(*storestate.Manager)
	if st := mgr.GetState(sdk.ExecModeFinalize); st != nil {
		return st.Context()
	}
	return application.NewContext(true)
}

// nextExecutionHeight matches BaseApp.validateFinalizeBlockHeight.
// After InitChain with initial height H+1, LastBlockHeight is still 0 and
// the first committed block is that initial height. Later blocks are last+1.
func nextExecutionHeight(application *App) int64 {
	last := application.LastBlockHeight()
	if last > 0 {
		return last + 1
	}
	initial := reflect.ValueOf(application.BaseApp).Elem().FieldByName("initialHeight").Int()
	if initial > 1 {
		return initial
	}
	return 1
}

func mustBal(t *testing.T, application *App, owner []byte, asset domain.AssetID) exchangetypes.Balance {
	t.Helper()
	bal, err := application.Keeper.GetBalance(qctx(application), owner, asset)
	require.NoError(t, err)
	return bal
}

func mustNonce(t *testing.T, application *App, owner []byte) uint64 {
	t.Helper()
	n, err := application.Keeper.GetCommandNonce(qctx(application), owner)
	require.NoError(t, err)
	return n
}

func requireCustody(t *testing.T, application *App) {
	t.Helper()
	ctx := qctx(application)
	for _, id := range []domain.AssetID{1, 2} {
		asset, err := application.Keeper.GetAsset(ctx, id)
		require.NoError(t, err)
		sum, err := application.Keeper.SumLiabilities(ctx, id)
		require.NoError(t, err)
		coin := application.BankKeeper.GetBalance(ctx, exchangekeeper.ModuleAddress(), asset.Denom)
		require.Truef(t, coin.Amount.GTE(sum), "asset %d custody %s liabilities %s", id, coin.Amount, sum)
	}
}
