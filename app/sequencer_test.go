package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/sequencer"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

func TestSequencerAdmissionAndFinalization(t *testing.T) {
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

	chain := &appChain{t: t, app: application, priv: sub.priv}
	cfg := sequencer.Config{
		ChainID:         testChainID,
		InstanceID:      []byte(exchangev1.DefaultInstanceID),
		Submitter:       sub.addr.String(),
		JournalPath:     filepath.Join(t.TempDir(), "journal"),
		MaxBatch:        100,
		BatchInterval:   time.Hour,
		MaxCommandBytes: 8192,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	svc, err := sequencer.Open(cfg, chain)
	require.NoError(t, err)
	ts := httptest.NewServer(svc.Handler())

	_, sell := signedPlace(t, alice, 1, exchangev1.Side_SIDE_SELL, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 150, 10)
	_, buy := signedPlace(t, bob, 1, exchangev1.Side_SIDE_BUY, exchangev1.TimeInForce_TIME_IN_FORCE_GTC, 100, 10)
	first := postCommand(t, ts.URL, sell)
	second := postCommand(t, ts.URL, buy)
	require.Equal(t, uint64(1), first.SequencerPosition)
	require.Equal(t, uint64(2), second.SequencerPosition)
	require.True(t, first.Provisional)
	require.True(t, second.Provisional)
	require.Equal(t, "pending", first.Status)

	require.NoError(t, svc.SubmitOnce(context.Background()))
	for _, pos := range []uint64{1, 2} {
		st, ok := svc.CommandStatus(pos)
		require.True(t, ok)
		require.Equal(t, sequencer.StatusFinalized, st)
	}
	require.Empty(t, svc.Pending())

	ctx := application.NewContext(true)
	batch, err := application.BatchKeeper.GetBatch(ctx, 1)
	require.NoError(t, err)
	wantID, err := canonical.HashBatchID(batch.Number, batch.PreRevision, []canonical.Command{sell, buy})
	require.NoError(t, err)
	require.Equal(t, wantID[:], batch.ID[:])
	require.Equal(t, uint64(1), batch.Number)
	require.Len(t, batch.Results, 2)

	trade, err := application.Keeper.GetTrade(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, domain.Price(10), trade.Price)
	require.Equal(t, domain.Quantity(100), trade.Quantity)
	require.Equal(t, exchangetypes.Balance{Available: 850, Locked: 50}, mustBal(t, application, alice.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 1000}, mustBal(t, application, alice.addr, 2))
	require.Equal(t, exchangetypes.Balance{Available: 100}, mustBal(t, application, bob.addr, 1))
	require.Equal(t, exchangetypes.Balance{Available: 9000}, mustBal(t, application, bob.addr, 2))
	requireCustody(t, application)

	ts.Close()
	require.NoError(t, svc.Close())
	restarted, err := sequencer.Open(cfg, chain)
	require.NoError(t, err)
	defer restarted.Close()
	require.Empty(t, restarted.Pending())
	require.Equal(t, uint64(3), restarted.NextPosition())
	for _, pos := range []uint64{1, 2} {
		st, ok := restarted.CommandStatus(pos)
		require.True(t, ok)
		require.Equal(t, sequencer.StatusFinalized, st)
	}
}

type appChain struct {
	t    *testing.T
	app  *App
	priv cryptotypes.PrivKey
	mu   sync.Mutex
}

func (c *appChain) Head(context.Context) (sequencer.Head, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx := c.app.NewContext(true)
	rev, err := c.app.Keeper.GetRevision(ctx)
	if err != nil {
		return sequencer.Head{}, err
	}
	batch, err := c.app.BatchKeeper.LatestBatch(ctx)
	if errors.Is(err, batchtypes.ErrNotFound) {
		return sequencer.Head{Revision: rev}, nil
	}
	if err != nil {
		return sequencer.Head{}, err
	}
	return sequencer.Head{Latest: batch.Number, Previous: batch.Commitment, Revision: rev}, nil
}

func (c *appChain) NextNonce(_ context.Context, owner []byte) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, err := c.app.Keeper.GetCommandNonce(c.app.NewContext(true), owner)
	if err != nil {
		return 0, err
	}
	return domain.ExpectedCommandNonce(last)
}

func (c *appChain) Submit(_ context.Context, msg *batchv1.MsgFinalizeBatch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	deliver(c.t, c.app, c.priv, msg, true)
	return nil
}

type commandPost struct {
	Accepted          bool   `json:"accepted"`
	SequencerPosition uint64 `json:"sequencer_position"`
	Status            string `json:"status"`
	Provisional       bool   `json:"provisional"`
	Error             string `json:"error"`
}

func postCommand(t *testing.T, base string, cmd canonical.Command) commandPost {
	t.Helper()
	res, err := http.Post(base+"/v1/commands", "application/json", bytes.NewReader(commandJSON(t, cmd)))
	require.NoError(t, err)
	defer res.Body.Close()
	var body commandPost
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equalf(t, http.StatusAccepted, res.StatusCode, "%s", body.Error)
	return body
}

func commandJSON(t *testing.T, cmd canonical.Command) []byte {
	t.Helper()
	side := "buy"
	if cmd.Place.Side == domain.SideSell {
		side = "sell"
	}
	payload := map[string]any{
		"protocol_version":     cmd.ProtocolVersion,
		"chain_id":             cmd.ChainID,
		"exchange_instance_id": string(cmd.ExchangeInstanceID),
		"owner":                sdk.AccAddress(cmd.Owner).String(),
		"command_nonce":        cmd.Nonce,
		"command_type":         "place",
		"place": map[string]any{
			"market_id":     cmd.Place.MarketID,
			"side":          side,
			"order_type":    "limit",
			"time_in_force": "gtc",
			"quantity_lots": cmd.Place.Quantity,
			"price_ticks":   cmd.Place.Price,
		},
		"pub_key":   hex.EncodeToString(cmd.PubKey),
		"signature": hex.EncodeToString(cmd.Signature),
	}
	bz, err := json.Marshal(payload)
	require.NoError(t, err)
	return bz
}
