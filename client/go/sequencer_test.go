package orderbook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/sequencer"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

func TestSequencerProvisionalAdmission(t *testing.T) {
	priv := testPriv()
	signer := privSigner{priv: priv}
	chainID := "orderbook-test"
	instance := []byte("orderbook-v1")
	client := &Client{chainID: chainID, instance: append([]byte(nil), instance...)}
	cmd, err := client.SignPlaceOrder(signer, 1, PlaceCommand{
		MarketID: 1, Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, QuantityLots: 4, PriceTicks: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := openSequencer(t, chainID, instance)
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	seq, err := NewSequencerClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := seq.Submit(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Accepted || !got.Provisional || got.SequencerPosition != 1 || got.Status != "pending" {
		t.Fatalf("admission %+v", got)
	}
	health, err := seq.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Pending != 1 {
		t.Fatalf("pending %d", health.Pending)
	}

	cmd.Signature[0] ^= 0xff
	if _, err := seq.Submit(context.Background(), cmd); !errors.Is(err, ErrAdmissionRejected) {
		t.Fatal(err)
	}
}

func TestSequencerRejectsNonUTF8Text(t *testing.T) {
	priv := testPriv()
	signer := privSigner{priv: priv}
	chainID := "orderbook-test"
	instance := []byte("orderbook-v1")
	client := &Client{chainID: chainID, instance: append([]byte(nil), instance...)}
	cmd, err := client.SignPlaceOrder(signer, 1, PlaceCommand{
		MarketID: 1, Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, QuantityLots: 1, PriceTicks: 10,
		ClientOrderID: []byte{0xff, 0xfe},
	})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := NewSequencerClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seq.Submit(context.Background(), cmd); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	cmd.Place.ClientOrderID = []byte("desk")
	cmd.ExchangeInstanceID = []byte{0xff}
	if _, err := seq.Submit(context.Background(), cmd); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestMarketOrderRequiresWorstPrice(t *testing.T) {
	c := &Client{}
	c.WithSigner(privSigner{priv: testPriv()})
	_, err := c.PlaceMarketOrder(context.Background(), MarketOrder{
		MarketID: 1, Side: domain.SideBuy, TimeInForce: domain.TimeInForceIOC,
		QuantityLots: 1, CommandNonce: 1,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	_, err = c.Deposit(context.Background(), "base", 0)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	bare := &Client{}
	if _, err := bare.Withdraw(context.Background(), "base", 1); !errors.Is(err, ErrSignerRequired) {
		t.Fatal(err)
	}
}

type admitChain struct{}

func (admitChain) Head(context.Context) (sequencer.Head, error) { return sequencer.Head{}, nil }

func (admitChain) NextNonce(context.Context, []byte) (uint64, error) { return 1, nil }

func (admitChain) Submit(context.Context, *batchv1.MsgFinalizeBatch) error { return nil }

func openSequencer(t *testing.T, chainID string, instance []byte) *sequencer.Service {
	t.Helper()
	svc, err := sequencer.Open(sequencer.Config{
		ChainID:         chainID,
		InstanceID:      instance,
		Submitter:       sdk.AccAddress(bytes20()).String(),
		JournalPath:     filepath.Join(t.TempDir(), "journal"),
		MaxCommandBytes: 8192,
		MaxBatch:        8,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, admitChain{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func bytes20() []byte {
	out := make([]byte, 20)
	for i := range out {
		out[i] = 1
	}
	return out
}

func TestSequencerClientRejectsEmptyURL(t *testing.T) {
	if _, err := NewSequencerClient("  "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"accepted":false,"provisional":false,"error":"bad"}`))
	}))
	defer srv.Close()
	seq, err := NewSequencerClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seq.Submit(context.Background(), canonical.Command{
		Owner: bytes20(), PubKey: bytes20(), Signature: make([]byte, 64), Type: canonical.CommandTypeCancel,
		Cancel: &canonical.Cancel{},
	}); !errors.Is(err, ErrAdmissionRejected) {
		t.Fatal(err)
	}
}
