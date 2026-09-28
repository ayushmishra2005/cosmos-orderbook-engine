package orderbook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cosmossdk.io/log/v2"
	sdkmath "cosmossdk.io/math"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/testutil/network"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/app"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/sequencer"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestClientEndToEnd(t *testing.T) {
	chainID := "orderbook-e2e"
	enc, err := app.MakeEncodingConfig(log.NewNopLogger())
	require.NoError(t, err)
	keyHome := t.TempDir()
	kr, err := keyring.New(sdk.KeyringServiceName(), keyring.BackendTest, keyHome, bytes.NewReader(nil), enc.Codec)
	require.NoError(t, err)
	alice := mustKey(t, kr, "alice")
	bob := mustKey(t, kr, "bob")
	submitter := mustKey(t, kr, "submitter")

	genesis := testGenesis(t, enc, chainID, []fundedAccount{
		{addr: alice, coins: coins("stake", 100_000_000, "base", 1_000_000)},
		{addr: bob, coins: coins("stake", 100_000_000, "quote", 1_000_000)},
		{addr: submitter, coins: coins("stake", 100_000_000)},
	}, submitter)
	cfg := network.DefaultConfig(func() network.TestFixture {
		return network.TestFixture{EncodingConfig: moduletestutil.TestEncodingConfig{
			InterfaceRegistry: enc.InterfaceRegistry,
			Codec:             enc.Codec,
			TxConfig:          enc.TxConfig,
			Amino:             enc.Amino,
		}}
	})
	cfg.GenesisState = genesis
	cfg.ChainID = chainID
	cfg.NumValidators = 1
	cfg.TimeoutCommit = 400 * time.Millisecond
	cfg.MinGasPrices = "0stake"
	cfg.CleanupDir = true
	cfg.EnableLogging = false
	cfg.AppConstructor = func(val network.ValidatorI) types.Application {
		application, err := app.New(val.GetCtx().Logger, dbm.NewMemDB(), chainID)
		if err != nil {
			panic(err)
		}
		return application
	}
	net, err := network.New(t, t.TempDir(), cfg)
	require.NoError(t, err)
	t.Cleanup(net.Cleanup)
	val := net.Validators[0]

	client, err := NewClient(Config{
		GRPCEndpoint: val.AppConfig.GRPC.Address,
		RPCEndpoint:  val.RPCAddress,
		ChainID:      chainID,
		Fees:         "1stake",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	waitMarket(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	market, err := client.GetMarket(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "base", market.BaseDenom)
	require.Equal(t, "quote", market.QuoteDenom)
	require.Equal(t, uint64(1), market.BaseLotSize)
	page, err := client.GetMarkets(ctx, 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Markets)
	rev, err := client.GetExchangeRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), rev)
	_, err = client.GetLatestBatch(ctx)
	require.ErrorIs(t, err, ErrNotFound)

	aliceSigner, err := NewKeyringSigner(kr, "alice")
	require.NoError(t, err)
	bobSigner, err := NewKeyringSigner(kr, "bob")
	require.NoError(t, err)
	client.WithSigner(aliceSigner)
	_, err = client.Deposit(ctx, "base", 1000)
	require.NoError(t, err)
	client.WithSigner(bobSigner)
	_, err = client.Deposit(ctx, "quote", 10_000)
	require.NoError(t, err)
	baseBal, err := client.GetBalance(ctx, aliceSigner.Address().String(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), baseBal.Available)
	bals, err := client.GetBalances(ctx, bobSigner.Address().String(), 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, bals.Balances)

	trades, err := client.SubscribeTrades(ctx, 1)
	require.NoError(t, err)
	other, err := client.SubscribeTrades(ctx, 2)
	require.NoError(t, err)
	orderStream, err := client.SubscribeOrders(ctx, 1)
	require.NoError(t, err)
	batchStream, err := client.SubscribeBatches(ctx)
	require.NoError(t, err)

	client.WithSigner(aliceSigner)
	sell, err := client.PlaceLimitOrder(ctx, LimitOrder{
		MarketID: 1, Side: domain.SideSell, TimeInForce: domain.TimeInForceGTC,
		QuantityLots: 50, PriceTicks: 10, CommandNonce: 1,
	})
	require.NoError(t, err)
	require.False(t, sell.OrderID.IsZero())
	require.Equal(t, wantOrderID(t, chainID, aliceSigner.Address(), 1, 1), sell.OrderID)

	client.WithSigner(bobSigner)
	bid, err := client.PlaceLimitOrder(ctx, LimitOrder{
		MarketID: 1, Side: domain.SideBuy, TimeInForce: domain.TimeInForceGTC,
		QuantityLots: 10, PriceTicks: 9, CommandNonce: 1,
	})
	require.NoError(t, err)
	book, err := client.GetOrderbook(ctx, 1, 10)
	require.NoError(t, err)
	require.Len(t, book.Asks, 1)
	require.Len(t, book.Bids, 1)
	require.Equal(t, uint64(10), book.Asks[0].PriceTicks)
	require.Equal(t, uint64(9), book.Bids[0].PriceTicks)
	open, err := client.GetOpenOrders(ctx, aliceSigner.Address().String(), 1, 10, 0)
	require.NoError(t, err)
	require.Len(t, open.Orders, 1)

	_, err = client.CancelOrder(ctx, Cancel{OrderID: bid.OrderID, CommandNonce: 2})
	require.NoError(t, err)
	_, err = client.PlaceLimitOrder(ctx, LimitOrder{
		MarketID: 1, Side: domain.SideBuy, TimeInForce: domain.TimeInForceGTC,
		QuantityLots: 30, PriceTicks: 10, CommandNonce: 3,
	})
	require.NoError(t, err)

	trade := waitFor(t, trades, 15*time.Second, func(ev Event) bool {
		return ev.Kind == KindTradeExecuted && ev.Trade != nil && ev.Trade.Sequence == 1
	})
	require.Equal(t, uint64(1), trade.Trade.MarketID)
	require.Equal(t, uint64(10), trade.Trade.PriceTicks)
	require.Equal(t, uint64(30), trade.Trade.QuantityLots)
	require.NotEmpty(t, trade.ID.TxHash)
	select {
	case ev := <-other.Events:
		t.Fatalf("market filter delivered %+v", ev.ID)
	case <-time.After(300 * time.Millisecond):
	}
	waitFor(t, orderStream, 15*time.Second, func(ev Event) bool {
		return ev.Order != nil && bytes.Equal(ev.Order.OrderID, sell.OrderID[:])
	})

	stored, err := client.GetOrder(ctx, sell.OrderID)
	require.NoError(t, err)
	require.Equal(t, uint64(20), stored.RemainingLots)
	gotTrades, err := client.GetTrades(ctx, 1, 0, 10)
	require.NoError(t, err)
	require.Len(t, gotTrades.Trades, 1)
	require.Equal(t, uint64(30), gotTrades.Trades[0].QuantityLots)
	require.Equal(t, uint64(10), gotTrades.Trades[0].PriceTicks)

	aliceBase, err := client.GetBalance(ctx, aliceSigner.Address().String(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(20), aliceBase.Locked)
	aliceQuote, err := client.GetBalance(ctx, aliceSigner.Address().String(), 2)
	require.NoError(t, err)
	require.Equal(t, uint64(300), aliceQuote.Available)
	bobBase, err := client.GetBalance(ctx, bobSigner.Address().String(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(30), bobBase.Available)

	client.WithSigner(aliceSigner)
	_, err = client.Withdraw(ctx, "quote", 300)
	require.NoError(t, err)
	aliceQuote, err = client.GetBalance(ctx, aliceSigner.Address().String(), 2)
	require.NoError(t, err)
	require.Equal(t, uint64(0), aliceQuote.Available)

	_, err = client.PlaceLimitOrder(ctx, LimitOrder{
		MarketID: 1, Side: domain.SideSell, TimeInForce: domain.TimeInForceGTC,
		QuantityLots: 10, PriceTicks: 15, CommandNonce: 2,
	})
	require.NoError(t, err)
	client.WithSigner(bobSigner)
	marketFill, err := client.PlaceMarketOrder(ctx, MarketOrder{
		MarketID: 1, Side: domain.SideBuy, TimeInForce: domain.TimeInForceIOC,
		QuantityLots: 10, WorstPriceTicks: 15, CommandNonce: 4,
	})
	require.NoError(t, err)
	require.False(t, marketFill.OrderID.IsZero())
	waitFor(t, trades, 15*time.Second, func(ev Event) bool {
		return ev.Trade != nil && ev.Trade.Sequence == 2 && ev.Trade.PriceTicks == 10 && ev.Trade.QuantityLots == 10
	})

	place, err := client.SignPlaceOrder(aliceSigner, 3, PlaceCommand{
		MarketID: 1, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, QuantityLots: 5, PriceTicks: 20,
	})
	require.NoError(t, err)
	buy, err := client.SignPlaceOrder(bobSigner, 5, PlaceCommand{
		MarketID: 1, Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, QuantityLots: 5, PriceTicks: 20,
	})
	require.NoError(t, err)
	seqURL, stopSeq := startSequencer(t, enc, chainID, keyHome, client.httpRPC, val.AppConfig.GRPC.Address)
	defer stopSeq()
	seqClient, err := NewSequencerClient(seqURL)
	require.NoError(t, err)
	sellAdmit, err := seqClient.Submit(ctx, place)
	require.NoError(t, err)
	require.True(t, sellAdmit.Provisional)
	require.True(t, sellAdmit.Accepted)
	buyAdmit, err := seqClient.Submit(ctx, buy)
	require.NoError(t, err)
	require.True(t, buyAdmit.Provisional)
	require.NotEqual(t, sellAdmit.SequencerPosition, buyAdmit.SequencerPosition)

	finalized := waitFor(t, batchStream, 30*time.Second, func(ev Event) bool {
		return ev.Kind == KindBatchFinalized && ev.Batch != nil && ev.Batch.Number == 1
	})
	require.Equal(t, uint64(2), finalized.Batch.CommandCount)
	require.Len(t, finalized.Batch.BatchID, 32)
	require.Len(t, finalized.Batch.Commitment, 32)
	require.NotZero(t, finalized.ID.Height)

	latest, err := client.GetLatestBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), latest.Number)
	require.Equal(t, finalized.Batch.BatchID, latest.ID[:])
	again, err := client.GetBatch(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, latest.Commitment, again.Commitment)
	byID, err := client.GetBatchByID(ctx, latest.ID)
	require.NoError(t, err)
	require.Equal(t, latest.Number, byID.Number)
	commitment, err := client.GetBatchCommitment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, latest.Commitment, commitment.Commitment)
	require.Equal(t, latest.ResultsHash, commitment.ResultsHash)
	var batchFills int
	for _, result := range latest.Results {
		batchFills += len(result.Trades)
	}
	require.Greater(t, batchFills, 0)
	gotTrades, err = client.GetTrades(ctx, 1, 0, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(gotTrades.Trades), 3)
}

type fundedAccount struct {
	addr  sdk.AccAddress
	coins sdk.Coins
}

func testGenesis(t *testing.T, enc app.EncodingConfig, chainID string, accounts []fundedAccount, submitter sdk.AccAddress) map[string]json.RawMessage {
	t.Helper()
	application, err := app.New(log.NewNopLogger(), dbm.NewMemDB(), chainID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = application.Close() })
	gen := application.DefaultGenesis()
	cdc := enc.Codec

	var auth authtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(gen[authtypes.ModuleName], &auth))
	var bank banktypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(gen[banktypes.ModuleName], &bank))
	packed := make([]authtypes.GenesisAccount, 0, len(accounts))
	for _, acc := range accounts {
		packed = append(packed, authtypes.NewBaseAccount(acc.addr, nil, 0, 0))
		bank.Balances = append(bank.Balances, banktypes.Balance{Address: acc.addr.String(), Coins: acc.coins})
	}
	anys, err := authtypes.PackAccounts(packed)
	require.NoError(t, err)
	auth.Accounts = append(auth.Accounts, anys...)
	bank.Supply = sdk.NewCoins()
	gen[authtypes.ModuleName] = cdc.MustMarshalJSON(&auth)
	gen[banktypes.ModuleName] = cdc.MustMarshalJSON(&bank)

	var batch batchv1.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(gen[batchtypes.ModuleName], &batch))
	batch.Submitter = submitter.String()
	gen[batchtypes.ModuleName] = cdc.MustMarshalJSON(&batch)
	if len(gen[exchangetypes.ModuleName]) == 0 {
		gen[exchangetypes.ModuleName] = cdc.MustMarshalJSON(exchangev1.DefaultGenesis())
	}
	return gen
}

func mustKey(t *testing.T, kr keyring.Keyring, name string) sdk.AccAddress {
	t.Helper()
	rec, _, err := kr.NewMnemonic(name, keyring.English, sdk.FullFundraiserPath, "", hd.Secp256k1)
	require.NoError(t, err)
	addr, err := rec.GetAddress()
	require.NoError(t, err)
	return addr
}

func coins(pairs ...any) sdk.Coins {
	out := sdk.NewCoins()
	for i := 0; i < len(pairs); i += 2 {
		out = out.Add(sdk.NewCoin(pairs[i].(string), sdkmath.NewInt(int64(pairs[i+1].(int)))))
	}
	return out
}

func wantOrderID(t *testing.T, chainID string, owner sdk.AccAddress, market, nonce uint64) domain.OrderID {
	t.Helper()
	id, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID: chainID, ExchangeInstanceID: []byte(exchangev1.DefaultInstanceID),
		Owner: owner, MarketID: domain.MarketID(market), CommandNonce: nonce,
	})
	require.NoError(t, err)
	return id
}

func waitMarket(t *testing.T, client *Client) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, last = client.GetMarket(ctx, 1)
		cancel()
		if last == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(last)
}

func waitFor(t *testing.T, sub *Subscription, d time.Duration, match func(Event) bool) Event {
	t.Helper()
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-sub.Events:
			if !ok {
				t.Fatal(sub.Err())
			}
			if match(ev) {
				return ev
			}
		case <-timer.C:
			t.Fatal("timed out waiting for event")
		}
	}
}

func startSequencer(t *testing.T, enc app.EncodingConfig, chainID, keyHome, rpc, grpc string) (string, func()) {
	t.Helper()
	_ = grpc
	chain, err := sequencer.NewCosmosChain(sequencer.CosmosConfig{
		Node: rpc, ChainID: chainID, Home: keyHome, KeyringBackend: keyring.BackendTest,
		FromName: "submitter", Fees: "1stake", Gas: 2_000_000,
		Codec: enc.Codec, InterfaceRegistry: enc.InterfaceRegistry, TxConfig: enc.TxConfig,
	})
	require.NoError(t, err)
	svc, err := sequencer.Open(sequencer.Config{
		ChainID: chainID, InstanceID: []byte(exchangev1.DefaultInstanceID),
		Submitter: chain.Submitter(), JournalPath: filepath.Join(t.TempDir(), "journal"),
		MaxCommandBytes: 8192, MaxBatch: 8, BatchInterval: 200 * time.Millisecond,
		RetryInitial: 200 * time.Millisecond, RetryMax: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, chain)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = svc.Run(runCtx)
		close(done)
	}()
	srv := httptest.NewServer(svc.Handler())
	return srv.URL, func() {
		cancel()
		srv.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		_ = svc.Close()
		_ = chain.Close()
	}
}
