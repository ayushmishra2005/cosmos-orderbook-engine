package sequencer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestCommandSizeLimit(t *testing.T) {
	cfg := testConfig(t)
	cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	encoded, err := encodeSigned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxCommandBytes = len(encoded)
	svc := openTest(t, cfg, nil)
	defer svc.Close()
	if _, err := svc.Admit(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	cfg2 := testConfig(t)
	cfg2.MaxCommandBytes = len(encoded) - 1
	svc2 := openTest(t, cfg2, nil)
	defer svc2.Close()
	if _, err := svc2.Admit(context.Background(), cmd); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if len(svc2.Pending()) != 0 {
		t.Fatal("oversized command was admitted")
	}
}

func TestManyInvalidSignatures(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	defer svc.Close()
	for i := 0; i < 64; i++ {
		cmd := signPlace(t, newKey(), cfg, 1, domain.SideBuy)
		cmd.Signature[0] ^= 0xff
		if _, err := svc.Admit(context.Background(), cmd); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
	}
	if len(svc.Pending()) != 0 || svc.NextPosition() != 1 {
		t.Fatalf("pending %d next %d", len(svc.Pending()), svc.NextPosition())
	}
}

func TestHTTPBodyLimit(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	defer svc.Close()
	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()

	exact := append([]byte("{"), bytes.Repeat([]byte(" "), maxRequestBody-1)...)
	res, err := http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(exact))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("near limit %d", res.StatusCode)
	}

	over := bytes.Repeat([]byte("a"), maxRequestBody+1)
	res, err = http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(over))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("over limit %d", res.StatusCode)
	}
	if len(svc.Pending()) != 0 {
		t.Fatal("rejected body was admitted")
	}

	cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	body := commandJSON(t, cmd)
	if len(body) >= maxRequestBody {
		t.Fatal("fixture is not a normal request")
	}
	res, err = http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("valid %d", res.StatusCode)
	}
}

func TestQueueLimitsAndOwnerIndex(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxOutstanding = 2
	cfg.MaxPerOwner = 2
	svc := openTest(t, cfg, nil)
	a, b, c := newKey(), newKey(), newKey()
	if _, err := svc.Admit(context.Background(), signPlace(t, a, cfg, 1, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, b, cfg, 1, domain.SideSell)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, c, cfg, 1, domain.SideBuy)); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}

	cfg = testConfig(t)
	cfg.MaxOutstanding = 10
	cfg.MaxPerOwner = 1
	svc = openTest(t, cfg, nil)
	alice, bob := newKey(), newKey()
	if _, err := svc.Admit(context.Background(), signPlace(t, alice, cfg, 1, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, alice, cfg, 2, domain.SideSell)); !errors.Is(err, ErrOwnerQueue) {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, bob, cfg, 1, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
}

func TestPendingPageAndNonceLookups(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxOutstanding = 20
	cfg.MaxPerOwner = 4
	cfg.MaxPendingPage = 2
	svc := openTest(t, cfg, nil)
	owners := make([]*secp256k1.PrivKey, 5)
	for i := range owners {
		owners[i] = newKey()
		if _, err := svc.Admit(context.Background(), signPlace(t, owners[i], cfg, 1, domain.SideBuy)); err != nil {
			t.Fatal(err)
		}
		if svc.LastNonceLookups() != 1 {
			t.Fatalf("lookups %d", svc.LastNonceLookups())
		}
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, owners[0], cfg, 2, domain.SideSell)); err != nil {
		t.Fatal(err)
	}
	if svc.LastNonceLookups() != 1 {
		t.Fatalf("owner lookups grew to %d", svc.LastNonceLookups())
	}
	page, next := svc.PendingPage(100, 0)
	if len(page) != 2 || next != 2 {
		t.Fatalf("page %d next %d", len(page), next)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pending?limit=1000&offset=0", nil)
	svc.Handler().ServeHTTP(rec, req)
	var body pendingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Commands) != 2 || body.NextOffset != 2 {
		t.Fatalf("%+v", body)
	}

	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err := Open(cfg, &memChain{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, err := svc.Admit(context.Background(), signPlace(t, owners[0], cfg, 3, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
	if svc.LastNonceLookups() != 2 {
		t.Fatalf("restart lookups %d", svc.LastNonceLookups())
	}
}

func TestSeenWindowDoesNotReexecuteConsumedNonce(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxSeen = 1
	alice, bob := newKey(), newKey()
	chain := &memChain{next: map[string]uint64{
		string(alice.PubKey().Address()): 1,
		string(bob.PubKey().Address()):   1,
	}}
	svc := openTest(t, cfg, chain)
	first := signPlace(t, alice, cfg, 1, domain.SideBuy)
	if _, err := svc.Admit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), first); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, bob, cfg, 1, domain.SideSell)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	chain.next[string(alice.PubKey().Address())] = 2
	chain.next[string(bob.PubKey().Address())] = 2
	chain.mu.Unlock()
	if _, err := svc.Admit(context.Background(), first); !errors.Is(err, ErrStaleNonce) {
		t.Fatalf("replay: %v", err)
	}
}

func TestQuarantineOwnerIndexStillBlocksSubmission(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxPerOwner = 4
	alice, bob := newKey(), newKey()
	chain := &memChain{next: map[string]uint64{}, submitErr: &batchtypes.CommandRejection{Index: 0, Err: exchangetypes.ErrNotFound}}
	chain.next[string(alice.PubKey().Address())] = 7
	var id domain.OrderID
	id[0] = 4
	bad := signCancel(t, alice, cfg, 7, id)
	later := signPlace(t, alice, cfg, 8, domain.SideSell)
	bobCmd := signPlace(t, bob, cfg, 1, domain.SideBuy)
	svc := openTest(t, cfg, chain)
	for _, cmd := range []canonical.Command{bad, later, bobCmd} {
		if _, err := svc.Admit(context.Background(), cmd); err != nil {
			t.Fatal(err)
		}
	}
	if svc.LastNonceLookups() != 1 {
		t.Fatalf("lookups %d", svc.LastNonceLookups())
	}
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrBatchRejected) {
		t.Fatal(err)
	}
	if st, _ := svc.CommandStatus(1); st != StatusQuarantined {
		t.Fatalf("status %s", st)
	}
	chain.submitErr = nil
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	got := chain.submitted[len(chain.submitted)-1]
	chain.mu.Unlock()
	if len(got.Commands) != 1 || got.Commands[0].CommandNonce != 1 {
		t.Fatalf("blocked owner was submitted %+v", got.Commands)
	}
	chain.mu.Lock()
	chain.next[string(alice.PubKey().Address())] = 8
	chain.mu.Unlock()
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	got = chain.submitted[len(chain.submitted)-1]
	chain.mu.Unlock()
	if len(got.Commands) != 1 || got.Commands[0].CommandNonce != 8 || got.Commands[0].Owner != sdk.AccAddress(alice.PubKey().Address()).String() {
		t.Fatalf("direct advance %+v", got.Commands)
	}
	chain.mu.Lock()
	chain.next[string(alice.PubKey().Address())] = 9
	chain.mu.Unlock()
	if _, err := svc.Admit(context.Background(), signPlace(t, alice, cfg, 8, domain.SideBuy)); !errors.Is(err, ErrStaleNonce) {
		t.Fatalf("consumed nonce: %v", err)
	}
	if _, err := svc.Admit(context.Background(), signPlace(t, alice, cfg, 9, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
}
