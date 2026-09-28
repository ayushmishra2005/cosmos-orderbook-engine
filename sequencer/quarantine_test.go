package sequencer

import (
	"context"
	"errors"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestQuarantineDoesNotBlockOtherOwners(t *testing.T) {
	cfg := testConfig(t)
	alice := newKey()
	bob := newKey()
	var id domain.OrderID
	id[0] = 9
	bad := signCancel(t, alice, cfg, 1, id)
	good := signPlace(t, bob, cfg, 1, domain.SideBuy)
	chain := &memChain{submitErr: &batchtypes.CommandRejection{Index: 0, Err: exchangetypes.ErrNotFound}}
	svc := openTest(t, cfg, chain)
	if _, err := svc.Admit(context.Background(), bad); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrBatchRejected) {
		t.Fatal(err)
	}
	if st, ok := svc.CommandStatus(1); !ok || st != StatusQuarantined {
		t.Fatalf("bad status %s %v", st, ok)
	}
	pending := svc.Pending()
	if len(pending) != 1 || pending[0].Position != 2 || pending[0].Status != StatusPending {
		t.Fatalf("pending %+v", pending)
	}

	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	chain.submitErr = nil
	svc, err := Open(cfg, chain)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if st, _ := svc.CommandStatus(1); st != StatusQuarantined {
		t.Fatalf("restart status %s", st)
	}
	pending = svc.Pending()
	if len(pending) != 1 || pending[0].Position != 2 {
		t.Fatalf("restart pending %+v", pending)
	}
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.CommandStatus(2); st != StatusFinalized {
		t.Fatalf("bob %s", st)
	}
	if st, _ := svc.CommandStatus(1); st != StatusQuarantined {
		t.Fatalf("bad became %s", st)
	}
	if len(svc.Pending()) != 0 {
		t.Fatalf("pending %+v", svc.Pending())
	}
}

func TestQuarantineBlocksLaterNonceOnly(t *testing.T) {
	cfg := testConfig(t)
	alice := newKey()
	bob := newKey()
	carol := newKey()
	chain := &memChain{next: map[string]uint64{}, submitErr: &batchtypes.CommandRejection{Index: 0, Err: exchangetypes.ErrNotFound}}
	chain.next[string(alice.PubKey().Address())] = 7
	var id domain.OrderID
	id[1] = 3
	bad := signCancel(t, alice, cfg, 7, id)
	bobCmd := signPlace(t, bob, cfg, 1, domain.SideBuy)
	later := signPlace(t, alice, cfg, 8, domain.SideSell)
	carolCmd := signPlace(t, carol, cfg, 1, domain.SideSell)
	svc := openTest(t, cfg, chain)
	for _, cmd := range []canonical.Command{bad, bobCmd, later, carolCmd} {
		if _, err := svc.Admit(context.Background(), cmd); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrBatchRejected) {
		t.Fatal(err)
	}
	if st, _ := svc.CommandStatus(1); st != StatusQuarantined {
		t.Fatalf("alice 7 %s", st)
	}
	chain.submitErr = nil
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	got := chain.submitted[len(chain.submitted)-1]
	chain.mu.Unlock()
	if len(got.Commands) != 2 {
		t.Fatalf("commands %d", len(got.Commands))
	}
	if got.Commands[0].Owner != sdk.AccAddress(bobCmd.Owner).String() || got.Commands[0].CommandNonce != 1 {
		t.Fatalf("first %+v", got.Commands[0])
	}
	if got.Commands[1].Owner != sdk.AccAddress(carolCmd.Owner).String() || got.Commands[1].CommandNonce != 1 {
		t.Fatalf("second %+v", got.Commands[1])
	}
	pending := svc.Pending()
	if len(pending) != 1 || pending[0].Position != 3 || pending[0].CommandNonce != 8 {
		t.Fatalf("alice 8 %+v", pending)
	}
	if st, _ := svc.CommandStatus(2); st != StatusFinalized {
		t.Fatalf("bob %s", st)
	}
	if st, _ := svc.CommandStatus(4); st != StatusFinalized {
		t.Fatalf("carol %s", st)
	}
}

func TestUnderfundedRejectionStaysPending(t *testing.T) {
	cfg := testConfig(t)
	chain := &memChain{submitErr: &batchtypes.CommandRejection{Index: 0, Err: exchangetypes.ErrInsufficientBalance}}
	svc := openTest(t, cfg, chain)
	cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	if _, err := svc.Admit(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrBatchRejected) {
		t.Fatal(err)
	}
	if st, _ := svc.CommandStatus(1); st != StatusPending {
		t.Fatalf("status %s", st)
	}
	chain.submitErr = nil
	chain.next = map[string]uint64{string(cmd.Owner): 1}
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.CommandStatus(1); st != StatusFinalized {
		t.Fatalf("after funding %s", st)
	}
}

func TestQuarantineYieldsAfterDirectNonceAdvance(t *testing.T) {
	cfg := testConfig(t)
	alice := newKey()
	bob := newKey()
	chain := &memChain{next: map[string]uint64{}, submitErr: &batchtypes.CommandRejection{Index: 0, Err: exchangetypes.ErrNotFound}}
	chain.next[string(alice.PubKey().Address())] = 7
	var missing domain.OrderID
	missing[0] = 4
	bad := signCancel(t, alice, cfg, 7, missing)
	later := signPlace(t, alice, cfg, 8, domain.SideSell)
	bobCmd := signPlace(t, bob, cfg, 1, domain.SideBuy)
	svc := openTest(t, cfg, chain)
	for _, cmd := range []canonical.Command{bad, later, bobCmd} {
		if _, err := svc.Admit(context.Background(), cmd); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrBatchRejected) {
		t.Fatal(err)
	}
	if st, _ := svc.CommandStatus(1); st != StatusQuarantined {
		t.Fatalf("quarantine %s", st)
	}
	if _, err := svc.Admit(context.Background(), bad); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	chain.submitErr = nil
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	bobBatch := chain.submitted[len(chain.submitted)-1]
	chain.mu.Unlock()
	if len(bobBatch.Commands) != 1 || bobBatch.Commands[0].Owner != sdk.AccAddress(bobCmd.Owner).String() {
		t.Fatalf("bob batch %+v", bobBatch.Commands)
	}
	if st, _ := svc.CommandStatus(3); st != StatusFinalized {
		t.Fatalf("bob %s", st)
	}
	pending := svc.Pending()
	if len(pending) != 1 || pending[0].CommandNonce != 8 {
		t.Fatalf("alice 8 still waiting %+v", pending)
	}

	chain.next[string(alice.PubKey().Address())] = 8
	if _, err := svc.Admit(context.Background(), bad); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate after chain advance: %v", err)
	}
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	aliceBatch := chain.submitted[len(chain.submitted)-1]
	chain.mu.Unlock()
	if len(aliceBatch.Commands) != 1 || aliceBatch.Commands[0].CommandNonce != 8 || aliceBatch.Commands[0].Owner != sdk.AccAddress(later.Owner).String() {
		t.Fatalf("alice 8 batch %+v", aliceBatch.Commands)
	}
	if st, _ := svc.CommandStatus(2); st != StatusFinalized {
		t.Fatalf("alice 8 %s", st)
	}
	if st, _ := svc.CommandStatus(1); st != StatusQuarantined {
		t.Fatalf("quarantine became %s", st)
	}
	if len(svc.Pending()) != 0 {
		t.Fatalf("pending %+v", svc.Pending())
	}
}

func TestRejectionLogClassifiesPermanent(t *testing.T) {
	err := rejectionError("failed to execute message; message index: 0: batch: command 0 rejected: exchange: not found")
	var rej *batchtypes.CommandRejection
	if !errors.As(err, &rej) || rej.Index != 0 || !errors.Is(rej.Err, exchangetypes.ErrNotFound) {
		t.Fatal(err)
	}
	if permanentCommand(exchangetypes.ErrInsufficientBalance) {
		t.Fatal("underfunded classified permanent")
	}
	if !permanentCommand(exchangetypes.ErrExpired) || !permanentCommand(exchangetypes.ErrWrongOwner) {
		t.Fatal("permanent sentinels")
	}
}

func signCancel(t *testing.T, priv *secp256k1.PrivKey, cfg Config, nonce uint64, id domain.OrderID) canonical.Command {
	t.Helper()
	return sign(t, priv, cfg, canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            cfg.ChainID,
		ExchangeInstanceID: append([]byte(nil), cfg.InstanceID...),
		Owner:              sdk.AccAddress(priv.PubKey().Address()),
		Nonce:              nonce,
		Type:               canonical.CommandTypeCancel,
		Cancel:             &canonical.Cancel{OrderID: id},
		PubKey:             priv.PubKey().Bytes(),
	})
}
