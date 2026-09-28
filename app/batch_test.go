package app

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
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
		PreviousBatchCommitment: make([]byte, 32),
		Commands:                []*batchv1.SignedCommand{sell, buy},
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
		PreviousBatchCommitment: make([]byte, 32),
		Commands:                []*batchv1.SignedCommand{sell, buy},
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

func signedCancel(t *testing.T, tr trader, nonce uint64, id domain.OrderID) (*batchv1.SignedCommand, canonical.Command) {
	t.Helper()
	cmd := canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            testChainID,
		ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner:              tr.addr,
		Nonce:              nonce,
		Type:               canonical.CommandTypeCancel,
		Cancel:             &canonical.Cancel{OrderID: id},
		PubKey:             tr.priv.PubKey().Bytes(),
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
		CommandType:        batchv1.CommandType_COMMAND_TYPE_CANCEL_ORDER,
		Cancel:             &batchv1.Cancel{OrderId: append([]byte(nil), id[:]...)},
		PubKey:             append([]byte(nil), cmd.PubKey...),
		Signature:          sig,
	}, cmd
}

func TestBatchCommitmentChain(t *testing.T) {
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
	sell, _ := signedPlace(t, alice, 1, exchangev1.Side_SIDE_SELL, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 150, 10)
	buy, _ := signedPlace(t, bob, 1, exchangev1.Side_SIDE_BUY, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 100, 10)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 1, ExpectedExchangeRevision: rev,
		PreviousBatchCommitment: make([]byte, 32),
		Commands:                []*batchv1.SignedCommand{sell, buy},
	}, true)

	ctx := application.NewContext(true)
	first, err := application.BatchKeeper.GetBatch(ctx, 1)
	require.NoError(t, err)
	require.True(t, first.Previous.IsZero())
	c1 := first.Commitment
	require.False(t, c1.IsZero())
	requireResults(t, application, first)

	cancel, _ := signedCancel(t, alice, 2, first.Results[0].OrderID)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 2, ExpectedExchangeRevision: first.PostRevision,
		PreviousBatchCommitment: append([]byte(nil), c1[:]...),
		Commands:                []*batchv1.SignedCommand{cancel},
	}, true)

	ctx = application.NewContext(true)
	second, err := application.BatchKeeper.GetBatch(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, c1, second.Previous)
	require.NotEqual(t, c1, second.Commitment)
	requireResults(t, application, second)
	params, err := application.BatchKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), params.Latest)
	require.Equal(t, second.Commitment, params.Head)

	qs := batchkeeper.NewQueryServer(application.BatchKeeper)
	q1, err := qs.Batch(ctx, &batchv1.QueryBatchRequest{BatchNumber: 1})
	require.NoError(t, err)
	require.Equal(t, first.ID[:], q1.Batch.BatchId)
	require.Equal(t, first.ResultsHash[:], q1.Batch.ResultsHash)
	require.Equal(t, c1[:], q1.Batch.BatchCommitment)
	require.Equal(t, make([]byte, 32), q1.Batch.PreviousBatchCommitment)
	q2, err := qs.Batch(ctx, &batchv1.QueryBatchRequest{BatchNumber: 2})
	require.NoError(t, err)
	require.Equal(t, second.ID[:], q2.Batch.BatchId)
	require.Equal(t, second.ResultsHash[:], q2.Batch.ResultsHash)
	require.Equal(t, c1[:], q2.Batch.PreviousBatchCommitment)
	require.Equal(t, second.Commitment[:], q2.Batch.BatchCommitment)
	latest, err := qs.LatestBatch(ctx, &batchv1.QueryLatestBatchRequest{})
	require.NoError(t, err)
	require.Equal(t, second.Commitment[:], latest.Batch.BatchCommitment)
	only, err := qs.BatchCommitment(ctx, &batchv1.QueryBatchCommitmentRequest{BatchNumber: 2})
	require.NoError(t, err)
	require.Equal(t, second.Commitment[:], only.BatchCommitment)
	require.Equal(t, second.ResultsHash[:], only.ResultsHash)

	beforeRev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	aliceBal := mustBal(t, application, alice.addr, 1)
	bobBal := mustBal(t, application, bob.addr, 2)
	bobNonce := mustNonce(t, application, bob.addr)
	rest, _ := signedPlace(t, bob, bobNonce+1, exchangev1.Side_SIDE_BUY, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 1, 10)
	deliver(t, application, sub.priv, &batchv1.MsgFinalizeBatch{
		Submitter: sub.addr.String(), BatchNumber: 3, ExpectedExchangeRevision: beforeRev,
		PreviousBatchCommitment: bytes.Repeat([]byte{0x11}, 32),
		Commands:                []*batchv1.SignedCommand{rest},
	}, false)

	ctx = application.NewContext(true)
	gotRev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeRev, gotRev)
	require.Equal(t, aliceBal, mustBal(t, application, alice.addr, 1))
	require.Equal(t, bobBal, mustBal(t, application, bob.addr, 2))
	require.Equal(t, bobNonce, mustNonce(t, application, bob.addr))
	params, err = application.BatchKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), params.Latest)
	require.Equal(t, second.Commitment, params.Head)
	_, err = application.BatchKeeper.GetBatch(ctx, 3)
	require.ErrorIs(t, err, batchtypes.ErrNotFound)
	requireCustody(t, application)
}

func requireResults(t *testing.T, application *App, batch batchtypes.Batch) {
	t.Helper()
	results := make([]canonical.Result, len(batch.Results))
	for i, result := range batch.Results {
		trades := make([]canonical.ResultTrade, len(result.Trades))
		for j, trade := range result.Trades {
			trades[j] = canonical.ResultTrade{MarketID: trade.MarketID, Sequence: trade.Sequence}
		}
		results[i] = canonical.Result{
			Index: result.Index, Type: canonical.CommandType(result.Type), Owner: result.Owner,
			OrderID: result.OrderID, Status: result.Status, Remaining: result.Remaining, Trades: trades,
		}
	}
	rh, err := canonical.HashResults(results)
	require.NoError(t, err)
	require.Equal(t, batchtypes.ResultsHash(rh), batch.ResultsHash)
	cm, err := canonical.HashBatchCommitment(canonical.BatchCommitmentInput{
		Version: canonical.BatchCommitmentVersion, ChainID: testChainID,
		ExchangeInstanceID: application.Keeper.InstanceID(), BatchNumber: batch.Number,
		BatchID: [32]byte(batch.ID), PreviousBatchCommitment: [32]byte(batch.Previous),
		ExecutionHeight: batch.Height, PreExchangeRevision: batch.PreRevision,
		PostExchangeRevision: batch.PostRevision, ResultsHash: rh,
	})
	require.NoError(t, err)
	require.Equal(t, batchtypes.BatchCommitment(cm), batch.Commitment)
}

// TestDeterministicReplay runs one multi-trader, multi-market fixture twice
// from identical genesis and compares keeper state, not event text.
func TestDeterministicReplay(t *testing.T) {
	fx := newReplayFixture()
	a := runReplay(t, fx)
	b := runReplay(t, fx)
	require.Equal(t, a, b)
	require.Greater(t, a.Revision, uint64(0))
	require.Len(t, a.Batches, 3)
	require.NotZero(t, a.TradeSeq[0])
	require.NotEmpty(t, a.Orders)
	require.False(t, a.Batches[0].ID.IsZero())
	require.False(t, a.Batches[0].ResultsHash.IsZero())
	require.False(t, a.Batches[0].Commitment.IsZero())
	require.Equal(t, a.Batches[0].Commitment, a.Batches[1].Previous)
	require.Equal(t, a.Batches[1].Commitment, a.Batches[2].Previous)
}

type replaySnap struct {
	Revision       uint64
	OrderSeq       [2]uint64
	TradeSeq       [2]uint64
	Nonces         []uint64
	Bals           []exchangetypes.Balance
	Orders         []string
	Trades         []string
	Batches        []batchtypes.Batch
	ExchangeDigest [32]byte
	BatchDigest    [32]byte
}

type replayFixture struct {
	makers []trader
	taker  trader
	sub    trader
}

func newReplayFixture() replayFixture {
	makers := make([]trader, 8)
	for i := range makers {
		makers[i] = replayTrader(byte(i + 1))
	}
	return replayFixture{makers: makers, taker: replayTrader(20), sub: replayTrader(21)}
}

func replayTrader(n byte) trader {
	var secret [32]byte
	secret[31] = n
	priv := secp256k1.GenPrivKeyFromSecret(secret[:])
	return trader{priv: priv, addr: sdk.AccAddress(priv.PubKey().Address())}
}

func runReplay(t *testing.T, fx replayFixture) replaySnap {
	t.Helper()
	accounts := []funded{{fx.sub, coins("stake", 1_000_000_000_000)}}
	for _, m := range fx.makers {
		accounts = append(accounts, funded{m, coins("stake", 1_000_000_000_000, "base", 1000, "quote", 1000)})
	}
	accounts = append(accounts, funded{fx.taker, coins("stake", 1_000_000_000_000, "quote", 100_000)})
	application := startReplayApp(t, accounts, fx.sub.addr)

	for _, m := range fx.makers {
		deliver(t, application, m.priv, &exchangev1.MsgDeposit{
			Owner: m.addr.String(), Amount: sdk.NewInt64Coin("base", 1000),
		}, true)
		deliver(t, application, m.priv, &exchangev1.MsgDeposit{
			Owner: m.addr.String(), Amount: sdk.NewInt64Coin("quote", 1000),
		}, true)
	}
	deliver(t, application, fx.taker.priv, &exchangev1.MsgDeposit{
		Owner: fx.taker.addr.String(), Amount: sdk.NewInt64Coin("quote", 100_000),
	}, true)

	for _, batch := range replayBatches(t, fx) {
		ctx := application.NewContext(true)
		rev, err := application.Keeper.GetRevision(ctx)
		require.NoError(t, err)
		var prev []byte
		if batch.number == 1 {
			prev = make([]byte, 32)
		} else {
			got, err := application.BatchKeeper.GetBatch(ctx, batch.number-1)
			require.NoError(t, err)
			prev = append([]byte(nil), got.Commitment[:]...)
		}
		deliver(t, application, fx.sub.priv, &batchv1.MsgFinalizeBatch{
			Submitter:                fx.sub.addr.String(),
			BatchNumber:              batch.number,
			ExpectedExchangeRevision: rev,
			PreviousBatchCommitment:  prev,
			Commands:                 batch.cmds,
		}, true)
	}
	return snapshotReplay(t, application, fx)
}

func startReplayApp(t *testing.T, accounts []funded, submitter sdk.AccAddress) *App {
	t.Helper()
	application, err := buildAppFull(t, accounts, func(gs *exchangev1.GenesisState) {
		gs.Markets = append(gs.Markets, &exchangev1.Market{
			Id: 2, BaseAssetId: 1, QuoteAssetId: 2,
			BaseLotSize: 1, QuoteAtomsPerTickPerLot: 1,
			MaxMakerVisits: 64, Enabled: true,
		})
	}, func(gs *batchv1.GenesisState) {
		gs.Submitter = submitter.String()
	})
	require.NoError(t, err)
	return application
}

type replayBatch struct {
	number uint64
	cmds   []*batchv1.SignedCommand
}

func replayBatches(t *testing.T, fx replayFixture) []replayBatch {
	t.Helper()
	var b1, b2, b3 []*batchv1.SignedCommand
	for _, m := range fx.makers {
		for k := uint64(1); k <= 8; k++ {
			b1 = append(b1, replayPlace(t, m, k, 1, exchangev1.Side_SIDE_SELL, 3, 20))
		}
	}
	for _, m := range fx.makers {
		for k := uint64(0); k < 8; k++ {
			b1 = append(b1, replayPlace(t, m, 9+k, 1, exchangev1.Side_SIDE_BUY, 1, 10))
		}
	}
	for n := uint64(1); n <= 64; n++ {
		b2 = append(b2, replayPlace(t, fx.taker, n, 1, exchangev1.Side_SIDE_BUY, 1, 20))
	}
	for i := 0; i < 4; i++ {
		m := fx.makers[i]
		for k := uint64(0); k < 8; k++ {
			id := replayOrderID(t, m.addr, 1, 9+k)
			b2 = append(b2, replayCancel(t, m, 17+k, id))
		}
	}
	for i := 4; i < 8; i++ {
		m := fx.makers[i]
		for k := uint64(0); k < 8; k++ {
			b2 = append(b2, replayPlace(t, m, 17+k, 2, exchangev1.Side_SIDE_SELL, 1, 40))
		}
	}
	for n := uint64(65); n <= 96; n++ {
		b3 = append(b3, replayPlace(t, fx.taker, n, 1, exchangev1.Side_SIDE_BUY, 1, 20))
	}
	for i := 4; i < 8; i++ {
		m := fx.makers[i]
		for k := uint64(0); k < 4; k++ {
			id := replayOrderID(t, m.addr, 1, 9+k)
			b3 = append(b3, replayCancel(t, m, 25+k, id))
		}
	}
	for i := 0; i < 4; i++ {
		m := fx.makers[i]
		for k := uint64(0); k < 4; k++ {
			b3 = append(b3, replayPlace(t, m, 25+k, 2, exchangev1.Side_SIDE_BUY, 1, 5))
		}
	}
	require.Len(t, b1, 128)
	require.Len(t, b2, 128)
	require.Len(t, b3, 64)
	return []replayBatch{{1, b1}, {2, b2}, {3, b3}}
}

func replayPlace(t *testing.T, tr trader, nonce, market uint64, side exchangev1.Side, qty, price uint64) *batchv1.SignedCommand {
	t.Helper()
	cmd, _ := replaySign(t, tr, canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            testChainID,
		ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner:              tr.addr,
		Nonce:              nonce,
		Type:               canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID: domain.MarketID(market), Side: domain.Side(side),
			Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
			Quantity: domain.Quantity(qty), Price: domain.Price(price),
		},
	})
	return cmd
}

func replayCancel(t *testing.T, tr trader, nonce uint64, id domain.OrderID) *batchv1.SignedCommand {
	t.Helper()
	cmd, _ := replaySign(t, tr, canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            testChainID,
		ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner:              tr.addr,
		Nonce:              nonce,
		Type:               canonical.CommandTypeCancel,
		Cancel:             &canonical.Cancel{OrderID: id},
	})
	return cmd
}

func replaySign(t *testing.T, tr trader, cmd canonical.Command) (*batchv1.SignedCommand, canonical.Command) {
	t.Helper()
	cmd.PubKey = tr.priv.PubKey().Bytes()
	bz, err := canonical.CommandSignBytes(cmd)
	require.NoError(t, err)
	sig, err := tr.priv.Sign(bz)
	require.NoError(t, err)
	cmd.Signature = sig
	pb := &batchv1.SignedCommand{
		ProtocolVersion:    cmd.ProtocolVersion,
		ChainId:            cmd.ChainID,
		ExchangeInstanceId: append([]byte(nil), cmd.ExchangeInstanceID...),
		Owner:              tr.addr.String(),
		CommandNonce:       cmd.Nonce,
		PubKey:             append([]byte(nil), cmd.PubKey...),
		Signature:          sig,
	}
	switch cmd.Type {
	case canonical.CommandTypePlace:
		pb.CommandType = batchv1.CommandType_COMMAND_TYPE_PLACE_ORDER
		pb.Place = &batchv1.Place{
			MarketId: uint64(cmd.Place.MarketID), Side: batchv1.Side(cmd.Place.Side),
			OrderType: batchv1.OrderType_ORDER_TYPE_LIMIT, TimeInForce: batchv1.TimeInForce_TIME_IN_FORCE_GTC,
			QuantityLots: uint64(cmd.Place.Quantity), PriceTicks: uint64(cmd.Place.Price),
		}
	case canonical.CommandTypeCancel:
		pb.CommandType = batchv1.CommandType_COMMAND_TYPE_CANCEL_ORDER
		pb.Cancel = &batchv1.Cancel{OrderId: append([]byte(nil), cmd.Cancel.OrderID[:]...)}
	default:
		t.Fatal(cmd.Type)
	}
	return pb, cmd
}

func replayOrderID(t *testing.T, owner sdk.AccAddress, market, nonce uint64) domain.OrderID {
	t.Helper()
	id, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID: testChainID, ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner: owner, MarketID: domain.MarketID(market), CommandNonce: nonce,
	})
	require.NoError(t, err)
	return id
}

func snapshotReplay(t *testing.T, application *App, fx replayFixture) replaySnap {
	t.Helper()
	ctx := application.NewContext(true)
	rev, err := application.Keeper.GetRevision(ctx)
	require.NoError(t, err)
	var snap replaySnap
	snap.Revision = rev
	traders := append(append([]trader{}, fx.makers...), fx.taker)
	for i, market := range []domain.MarketID{1, 2} {
		seq, err := application.Keeper.GetOrderSequence(ctx, market)
		require.NoError(t, err)
		tradeSeq, err := application.Keeper.GetTradeSequence(ctx, market)
		require.NoError(t, err)
		snap.OrderSeq[i] = seq
		snap.TradeSeq[i] = tradeSeq
		for n := uint64(1); n <= tradeSeq; n++ {
			trade, err := application.Keeper.GetTrade(ctx, market, n)
			require.NoError(t, err)
			snap.Trades = append(snap.Trades, fmt.Sprintf("%d %d %d %d %d %d %x %x %x %x",
				trade.MarketID, trade.Sequence, trade.Price, trade.Quantity, trade.BaseAmount, trade.QuoteAmount,
				trade.MakerOrderID[:], trade.TakerOrderID[:], trade.Buyer, trade.Seller))
		}
	}
	qs := exchangekeeper.NewQueryServer(application.Keeper)
	for _, tr := range traders {
		nonce, err := application.Keeper.GetCommandNonce(ctx, tr.addr)
		require.NoError(t, err)
		snap.Nonces = append(snap.Nonces, nonce)
		for _, asset := range []domain.AssetID{1, 2} {
			snap.Bals = append(snap.Bals, mustBal(t, application, tr.addr, asset))
		}
		for _, market := range []uint64{1, 2} {
			var off uint64
			for pages := 0; pages < 8; pages++ {
				res, err := qs.OpenOrders(ctx, &exchangev1.QueryOpenOrdersRequest{
					Owner: tr.addr.String(), MarketId: market, Offset: off, Limit: 100,
				})
				require.NoError(t, err)
				for _, order := range res.Orders {
					snap.Orders = append(snap.Orders, fmt.Sprintf("%s %s %d %d %d %d %d",
						hex.EncodeToString(order.OrderId), order.Owner, order.MarketId, order.Side,
						order.PriceTicks, order.RemainingLots, order.Sequence))
				}
				if res.NextOffset == 0 || res.NextOffset == off {
					break
				}
				off = res.NextOffset
			}
		}
	}
	for n := uint64(1); n <= 3; n++ {
		batch, err := application.BatchKeeper.GetBatch(ctx, n)
		require.NoError(t, err)
		snap.Batches = append(snap.Batches, batch)
	}
	snap.ExchangeDigest, err = application.Keeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	snap.BatchDigest, err = application.BatchKeeper.SnapshotDigest(ctx)
	require.NoError(t, err)
	require.NoError(t, application.Keeper.CheckInvariants(ctx))
	require.NoError(t, application.BatchKeeper.CheckInvariants(ctx))
	return snap
}
