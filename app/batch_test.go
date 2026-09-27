package app

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchkeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/keeper"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestBatchExecution(t *testing.T) {
	alice := newTrader()
	bob := newTrader()
	sub := newTrader()
	application := startBatchApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 1000)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 10_000)},
		{sub, coins("stake", 1_000_000_000_000)},
	}, sub.addr)

	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 1000),
	}, true)
	deliver(t, application, bob.priv, &exchangev1.MsgDeposit{
		Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 10_000),
	}, true)

	rev, err := application.Keeper.GetRevision(application.NewContext(true))
	require.NoError(t, err)
	sell, sellCmd := signedPlace(t, alice, 1, exchangev1.Side_SIDE_SELL, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 150, 10)
	buy, buyCmd := signedPlace(t, bob, 1, exchangev1.Side_SIDE_BUY, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 100, 10)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 1, ExpectedExchangeRevision: rev,
		Commands: []*batchv1.SignedCommand{sell, buy},
	}, true)

	ctx := application.NewContext(true)
	aliceSell, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID: testChainID, ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner: alice.addr, MarketID: 1, CommandNonce: 1,
	})
	require.NoError(t, err)
	stored, err := application.Keeper.GetOrder(ctx, aliceSell)
	require.NoError(t, err)
	require.Equal(t, domain.Quantity(50), stored.Order.RemainingQuantity)
	require.Equal(t, domain.Sequence(1), stored.Order.Sequence)

	bobBuy, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID: testChainID, ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner: bob.addr, MarketID: 1, CommandNonce: 1,
	})
	require.NoError(t, err)
	_, err = application.Keeper.GetOrder(ctx, bobBuy)
	require.ErrorIs(t, err, exchangetypes.ErrNotFound)

	trade, err := application.Keeper.GetTrade(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, domain.Price(10), trade.Price)
	require.Equal(t, domain.Quantity(100), trade.Quantity)
	require.Equal(t, exchangetypes.Balance{Available: 850, Locked: 50}, mustBal(t, application, alice.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 1000}, mustBal(t, application, alice.addr, 2))
	require.Equal(t, exchangetypes.Balance{Available: 100}, mustBal(t, application, bob.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 9000}, mustBal(t, application, bob.addr, 2))
	require.Equal(t, uint64(1), mustNonce(t, application, alice.addr))
	require.Equal(t, uint64(1), mustNonce(t, application, bob.addr))
	gotRev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, rev+2, gotRev)

	batch, err := application.BatchKeeper.GetBatch(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), batch.Number)
	require.Equal(t, rev, batch.PreRevision)
	require.Equal(t, rev+2, batch.PostRevision)
	require.Len(t, batch.Results, 2)
	require.Equal(t, batchtypes.StatusResting, batch.Results[0].Status)
	require.Equal(t, batchtypes.StatusFilled, batch.Results[1].Status)
	require.Equal(t, []batchtypes.TradeRef{{MarketID: 1, Sequence: 1}}, batch.Results[1].Trades)
	wantID, err := canonical.HashBatchID(1, rev, []canonical.Command{sellCmd, buyCmd})
	require.NoError(t, err)
	require.Equal(t, batchtypes.BatchID(wantID), batch.ID)
	byID, err := application.BatchKeeper.GetBatchByID(ctx, batch.ID)
	require.NoError(t, err)
	require.Equal(t, batch.Number, byID.Number)

	qs := batchkeeper.NewQueryServer(application.BatchKeeper)
	latest, err := qs.LatestBatch(ctx, &batchv1.QueryLatestBatchRequest{})
	require.NoError(t, err)
	require.Equal(t, batch.ID[:], latest.Batch.BatchId)
	requireCustody(t, application)
}

func TestBatchFailureLeavesExchangeUnchanged(t *testing.T) {
	alice := newTrader()
	bob := newTrader()
	sub := newTrader()
	application := startBatchApp(t, []funded{
		{alice, coins("stake", 1_000_000_000_000, "base", 1000)},
		{bob, coins("stake", 1_000_000_000_000, "quote", 10_000)},
		{sub, coins("stake", 1_000_000_000_000)},
	}, sub.addr)

	deliver(t, application, alice.priv, &exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 1000),
	}, true)
	deliver(t, application, bob.priv, &exchangev1.MsgDeposit{
		Owner: bob.addr.String(), Amount: sdk.NewInt64Coin("quote", 10_000),
	}, true)

	ctx := application.NewContext(true)
	rev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	aliceBank := application.BankKeeper.GetBalance(ctx, alice.addr, "base").Amount
	bobBank := application.BankKeeper.GetBalance(ctx, bob.addr, "quote").Amount
	sell, _ := signedPlace(t, alice, 1, exchangev1.Side_SIDE_SELL, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 10, 10)
	buy, _ := signedPlace(t, bob, 1, exchangev1.Side_SIDE_BUY, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1001, 10)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 1, ExpectedExchangeRevision: rev,
		Commands: []*batchv1.SignedCommand{sell, buy},
	}, false)

	ctx = application.NewContext(true)
	gotRev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, rev, gotRev)
	require.Equal(t, uint64(0), mustNonce(t, application, alice.addr))
	require.Equal(t, uint64(0), mustNonce(t, application, bob.addr))
	require.Equal(t, exchangetypes.Balance{Available: 1000}, mustBal(t, application, alice.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 10_000}, mustBal(t, application, bob.addr, 2))
	_, err = application.BatchKeeper.LatestBatch(ctx)
	require.ErrorIs(t, err, batchtypes.ErrNotFound)
	seq, err := application.Keeper.GetOrderSequence(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(0), seq)
	tradeSeq, err := application.Keeper.GetTradeSequence(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(0), tradeSeq)
	_, err = application.Keeper.GetTrade(ctx, 1, 1)
	require.ErrorIs(t, err, exchangetypes.ErrNotFound)
	require.True(t, aliceBank.Equal(application.BankKeeper.GetBalance(ctx, alice.addr, "base").Amount))
	require.True(t, bobBank.Equal(application.BankKeeper.GetBalance(ctx, bob.addr, "quote").Amount))
	require.True(t, sdkmath.NewInt(1000).Equal(application.BankKeeper.GetBalance(ctx, exchangekeeper.ModuleAddress(), "base").Amount))
	requireCustody(t, application)
}

func startBatchApp(t *testing.T, accounts []funded, submitter sdk.AccAddress) *App {
	t.Helper()
	application, err := buildAppFull(t, accounts, nil, func(gs *batchv1.GenesisState) {
		gs.Submitter = submitter.String()
	})
	require.NoError(t, err)
	return application
}

func signedPlace(t *testing.T, tr trader, nonce uint64, side exchangev1.Side, tif exchangev1.TimeInForce, qty, price uint64) (*batchv1.SignedCommand, canonical.Command) {
	t.Helper()
	cmd := canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            testChainID,
		ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner:              tr.addr,
		Nonce:              nonce,
		Type:               canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID:    1,
			Side:        domain.Side(side),
			Type:        domain.OrderTypeLimit,
			TimeInForce: domain.TimeInForce(tif),
			Quantity:    domain.Quantity(qty),
			Price:       domain.Price(price),
		},
		PubKey: tr.priv.PubKey().Bytes(),
	}
	bz, err := canonical.CommandSignBytes(cmd)
	require.NoError(t, err)
	sig, err := tr.priv.Sign(bz)
	require.NoError(t, err)
	cmd.Signature = sig
	return &batchv1.SignedCommand{
		ProtocolVersion:    cmd.ProtocolVersion,
		ChainId:            cmd.ChainID,
		ExchangeInstanceId: append([]byte(nil), cmd.ExchangeInstanceID...),
		Owner:              tr.addr.String(),
		CommandNonce:       nonce,
		CommandType:        batchv1.CommandType_COMMAND_TYPE_PLACE_ORDER,
		Place: &batchv1.Place{
			MarketId: 1, Side: batchv1.Side(side), OrderType: batchv1.OrderType_ORDER_TYPE_LIMIT,
			TimeInForce: batchv1.TimeInForce(tif), QuantityLots: qty, PriceTicks: price,
		},
		PubKey:    append([]byte(nil), cmd.PubKey...),
		Signature: sig,
	}, cmd
}
