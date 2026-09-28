package sequencer

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// Chain reads batch head, exchange revision, and account nonces, and submits
// MsgFinalizeBatch. The sequencer does not open keeper state itself.
type Chain interface {
	Head(ctx context.Context) (Head, error)
	NextNonce(ctx context.Context, owner []byte) (uint64, error)
	Submit(ctx context.Context, msg *batchv1.MsgFinalizeBatch) error
}

// CosmosConfig selects the chain RPC, keyring, and submitter.
// The key stays in the Cosmos SDK keyring.
type CosmosConfig struct {
	Node              string
	ChainID           string
	Home              string
	KeyringBackend    string
	FromName          string
	Fees              string
	Gas               uint64
	Codec             codec.Codec
	InterfaceRegistry codectypes.InterfaceRegistry
	TxConfig          client.TxConfig
}

// CosmosChain submits batches with the normal SDK transaction flow.
type CosmosChain struct {
	node      *rpchttp.HTTP
	client    client.Context
	submitter string
	fromName  string
	gas       uint64
	fees      string
	chainID   string
}

// NewCosmosChain opens the RPC client and the submitter key.
func NewCosmosChain(cfg CosmosConfig) (*CosmosChain, error) {
	if cfg.Codec == nil || cfg.TxConfig == nil || cfg.InterfaceRegistry == nil {
		return nil, fmt.Errorf("sequencer: encoding config is required")
	}
	if cfg.ChainID == "" || cfg.FromName == "" || cfg.Home == "" {
		return nil, fmt.Errorf("sequencer: chain id, home, and key name are required")
	}
	nodeURL, err := normalizeNode(cfg.Node)
	if err != nil {
		return nil, err
	}
	backend := cfg.KeyringBackend
	if backend == "" {
		backend = keyring.BackendTest
	}
	kr, err := keyring.New(sdk.KeyringServiceName(), backend, cfg.Home, os.Stdin, cfg.Codec)
	if err != nil {
		return nil, fmt.Errorf("sequencer: keyring: %w", err)
	}
	rec, err := kr.Key(cfg.FromName)
	if err != nil {
		return nil, fmt.Errorf("sequencer: key %s: %w", cfg.FromName, err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("sequencer: key %s: %w", cfg.FromName, err)
	}
	fees := cfg.Fees
	if fees == "" {
		fees = "1stake"
	}
	if _, err := sdk.ParseCoinsNormalized(fees); err != nil {
		return nil, fmt.Errorf("sequencer: fees: %w", err)
	}
	gas := cfg.Gas
	if gas == 0 {
		gas = 2_000_000
	}
	node, err := client.NewClientFromNode(nodeURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChainUnavailable, err)
	}
	if err := node.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChainUnavailable, err)
	}
	base := client.Context{}.
		WithCodec(cfg.Codec).
		WithInterfaceRegistry(cfg.InterfaceRegistry).
		WithTxConfig(cfg.TxConfig).
		WithKeyring(kr).
		WithChainID(cfg.ChainID).
		WithFromName(cfg.FromName).
		WithFromAddress(addr).
		WithBroadcastMode(flags.BroadcastSync).
		WithSkipConfirmation(true).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithClient(node).
		WithHomeDir(cfg.Home).
		WithNodeURI(nodeURL)
	return &CosmosChain{
		node:      node,
		client:    base,
		submitter: addr.String(),
		fromName:  cfg.FromName,
		gas:       gas,
		fees:      fees,
		chainID:   cfg.ChainID,
	}, nil
}

// Submitter is the bech32 address of the configured batch submitter.
func (c *CosmosChain) Submitter() string { return c.submitter }

// Close stops the RPC client.
func (c *CosmosChain) Close() error {
	if c == nil || c.node == nil {
		return nil
	}
	err := c.node.Stop()
	c.node = nil
	return err
}

// Head reads the latest finalized batch and the exchange revision.
func (c *CosmosChain) Head(ctx context.Context) (Head, error) {
	qctx, err := c.queryCtx(ctx)
	if err != nil {
		return Head{}, err
	}
	revRes, err := exchangev1.NewQueryClient(qctx).ExchangeRevision(ctx, &exchangev1.QueryExchangeRevisionRequest{})
	if err != nil {
		return Head{}, fmt.Errorf("%w: revision: %v", ErrChainUnavailable, err)
	}
	latest, err := batchv1.NewQueryClient(qctx).LatestBatch(ctx, &batchv1.QueryLatestBatchRequest{})
	if err != nil {
		if isBatchNotFound(err) {
			return Head{Revision: revRes.Revision}, nil
		}
		return Head{}, fmt.Errorf("%w: latest batch: %v", ErrChainUnavailable, err)
	}
	if latest.Batch == nil {
		return Head{Revision: revRes.Revision}, nil
	}
	if len(latest.Batch.BatchCommitment) != 32 {
		return Head{}, fmt.Errorf("%w: commitment length", ErrHead)
	}
	var previous [32]byte
	copy(previous[:], latest.Batch.BatchCommitment)
	return Head{
		Latest:   latest.Batch.BatchNumber,
		Previous: previous,
		Revision: revRes.Revision,
	}, nil
}

// NextNonce is one past the owner's last accepted exchange nonce.
// A missing key means the next nonce is 1. A lower command nonce can still
// become stale before the batch executes.
func (c *CosmosChain) NextNonce(ctx context.Context, owner []byte) (uint64, error) {
	qctx, err := c.queryCtx(ctx)
	if err != nil {
		return 0, err
	}
	key, err := canonical.EncodeAccountNonceKey(owner)
	if err != nil {
		return 0, err
	}
	bz, _, err := qctx.QueryStore(key, exchangetypes.StoreKey)
	if err != nil {
		return 0, fmt.Errorf("%w: nonce: %v", ErrChainUnavailable, err)
	}
	var last uint64
	if len(bz) != 0 {
		last, err = exchangetypes.DecodeUint64(bz)
		if err != nil {
			return 0, fmt.Errorf("sequencer: nonce value: %w", err)
		}
	}
	next, err := domain.ExpectedCommandNonce(last)
	if err != nil {
		return 0, err
	}
	return next, nil
}

// Submit signs MsgFinalizeBatch with the submitter key and waits for inclusion.
func (c *CosmosChain) Submit(ctx context.Context, msg *batchv1.MsgFinalizeBatch) error {
	if msg == nil {
		return ErrMalformed
	}
	if msg.Submitter != c.submitter {
		return fmt.Errorf("sequencer: message submitter does not match the configured key")
	}
	qctx, err := c.queryCtx(ctx)
	if err != nil {
		return err
	}
	txf := tx.Factory{}.
		WithChainID(c.chainID).
		WithKeybase(c.client.Keyring).
		WithTxConfig(c.client.TxConfig).
		WithAccountRetriever(c.client.AccountRetriever).
		WithFromName(c.fromName).
		WithGas(c.gas).
		WithFees(c.fees).
		WithSignMode(signing.SignMode_SIGN_MODE_DIRECT).
		WithTimeoutHeight(uint64(qctx.Height) + 30)
	txf, err = txf.Prepare(qctx)
	if err != nil {
		return fmt.Errorf("%w: account: %v", ErrChainUnavailable, err)
	}
	builder, err := txf.BuildUnsignedTx(msg)
	if err != nil {
		return fmt.Errorf("sequencer: build tx: %w", err)
	}
	if err := tx.Sign(ctx, txf, c.fromName, builder, true); err != nil {
		return fmt.Errorf("sequencer: sign tx: %w", err)
	}
	txBytes, err := c.client.TxConfig.TxEncoder()(builder.GetTx())
	if err != nil {
		return fmt.Errorf("sequencer: encode tx: %w", err)
	}
	res, err := qctx.BroadcastTxSync(txBytes)
	if err != nil {
		return fmt.Errorf("%w: broadcast: %v", ErrChainUnavailable, err)
	}
	if res.Code != 0 {
		return rejectionError(fmt.Sprintf("checktx code %d: %s", res.Code, res.RawLog))
	}
	hash, err := hex.DecodeString(res.TxHash)
	if err != nil || len(hash) == 0 {
		return fmt.Errorf("%w: broadcast hash %q", ErrBatchRejected, res.TxHash)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := c.node.Tx(ctx, hash, false)
		if err == nil {
			if result.TxResult.Code != 0 {
				return rejectionError(fmt.Sprintf("code %d: %s", result.TxResult.Code, result.TxResult.Log))
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: timed out waiting for %s: %v", ErrChainUnavailable, res.TxHash, err)
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *CosmosChain) queryCtx(ctx context.Context) (client.Context, error) {
	st, err := c.node.Status(ctx)
	if err != nil {
		return client.Context{}, fmt.Errorf("%w: %v", ErrChainUnavailable, err)
	}
	height := st.SyncInfo.LatestBlockHeight
	if height <= 0 {
		return client.Context{}, fmt.Errorf("%w: no committed block", ErrChainUnavailable)
	}
	return c.client.WithHeight(height).WithCmdContext(ctx), nil
}

func normalizeNode(raw string) (string, error) {
	if raw == "" {
		raw = "http://127.0.0.1:26657"
	}
	switch {
	case strings.HasPrefix(raw, "tcp://"):
		raw = "http://" + strings.TrimPrefix(raw, "tcp://")
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
	default:
		raw = "http://" + raw
	}
	return raw, nil
}

func isBatchNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found")
}
