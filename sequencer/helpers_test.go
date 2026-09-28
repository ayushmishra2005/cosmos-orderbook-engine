package sequencer

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ChainID:         "orderbook-test",
		InstanceID:      []byte("orderbook-v1"),
		Submitter:       sdk.AccAddress(bytes20(1)).String(),
		JournalPath:     filepath.Join(t.TempDir(), "journal"),
		MaxCommandBytes: 8192,
		MaxBatch:        100,
		BatchInterval:   time.Hour,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func bytes20(b byte) []byte {
	out := make([]byte, 20)
	for i := range out {
		out[i] = b
	}
	return out
}

func newKey() *secp256k1.PrivKey { return secp256k1.GenPrivKey() }

func signPlace(t *testing.T, priv *secp256k1.PrivKey, cfg Config, nonce uint64, side domain.Side) canonical.Command {
	t.Helper()
	return sign(t, priv, cfg, canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            cfg.ChainID,
		ExchangeInstanceID: append([]byte(nil), cfg.InstanceID...),
		Owner:              sdk.AccAddress(priv.PubKey().Address()),
		Nonce:              nonce,
		Type:               canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID:    1,
			Side:        side,
			Type:        domain.OrderTypeLimit,
			TimeInForce: domain.TimeInForceGTC,
			Quantity:    10,
			Price:       10,
		},
		PubKey: priv.PubKey().Bytes(),
	})
}

func sign(t *testing.T, priv *secp256k1.PrivKey, cfg Config, cmd canonical.Command) canonical.Command {
	t.Helper()
	_ = cfg
	bz, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := priv.Sign(bz)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Signature = sig
	return cmd
}

func openTest(t *testing.T, cfg Config, chain Chain) *Service {
	t.Helper()
	if chain == nil {
		chain = &memChain{}
	}
	svc, err := Open(cfg, chain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

type memChain struct {
	mu        sync.Mutex
	head      Head
	next      map[string]uint64
	nonceErr  error
	headErr   error
	submitErr error
	submitted []*batchv1.MsgFinalizeBatch
	onSubmit  func()
}

func (m *memChain) Head(context.Context) (Head, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.headErr != nil {
		return Head{}, m.headErr
	}
	return m.head, nil
}

func (m *memChain) NextNonce(_ context.Context, owner []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nonceErr != nil {
		return 0, m.nonceErr
	}
	if m.next != nil {
		if n, ok := m.next[string(owner)]; ok {
			return n, nil
		}
	}
	return 1, nil
}

func (m *memChain) Submit(_ context.Context, msg *batchv1.MsgFinalizeBatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submitted = append(m.submitted, msg)
	if m.onSubmit != nil {
		m.onSubmit()
	}
	return m.submitErr
}
