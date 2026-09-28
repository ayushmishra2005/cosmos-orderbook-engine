package sequencer

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestAdmitValidAndMonotonic(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	priv := newKey()
	var positions []uint64
	for nonce := uint64(1); nonce <= 3; nonce++ {
		rec, err := svc.Admit(context.Background(), signPlace(t, priv, cfg, nonce, domain.SideBuy))
		if err != nil {
			t.Fatal(err)
		}
		if !rec.Provisional || rec.Status != StatusPending {
			t.Fatalf("receipt %+v", rec)
		}
		positions = append(positions, rec.Position)
	}
	if positions[0] != 1 || positions[1] != 2 || positions[2] != 3 {
		t.Fatalf("positions %v", positions)
	}
	if svc.NextPosition() != 4 {
		t.Fatalf("next %d", svc.NextPosition())
	}
}

func TestAdmitRejects(t *testing.T) {
	cfg := testConfig(t)
	chain := &memChain{}
	svc := openTest(t, cfg, chain)
	priv := newKey()
	other := newKey()
	valid := signPlace(t, priv, cfg, 1, domain.SideSell)

	badSig := valid
	badSig.Signature = append([]byte(nil), valid.Signature...)
	badSig.Signature[0] ^= 0xff
	if _, err := svc.Admit(context.Background(), badSig); !errors.Is(err, ErrSignature) {
		t.Fatalf("signature: %v", err)
	}

	mismatch := signPlace(t, priv, cfg, 1, domain.SideSell)
	mismatch.Owner = sdk.AccAddress(other.PubKey().Address())
	mismatch = sign(t, priv, cfg, mismatch)
	if _, err := svc.Admit(context.Background(), mismatch); !errors.Is(err, ErrPubKeyMismatch) {
		t.Fatalf("owner: %v", err)
	}

	wrongChain := valid
	wrongChain.ChainID = "other-chain"
	wrongChain = sign(t, priv, cfg, wrongChain)
	if _, err := svc.Admit(context.Background(), wrongChain); !errors.Is(err, ErrChainID) {
		t.Fatalf("chain: %v", err)
	}

	wrongInstance := valid
	wrongInstance.ExchangeInstanceID = []byte("other-instance")
	wrongInstance = sign(t, priv, cfg, wrongInstance)
	if _, err := svc.Admit(context.Background(), wrongInstance); !errors.Is(err, ErrInstance) {
		t.Fatalf("instance: %v", err)
	}

	unsupported := valid
	unsupported.Type = 9
	unsupported.Place = nil
	if _, err := svc.Admit(context.Background(), unsupported); !errors.Is(err, ErrUnsupportedCommand) {
		t.Fatalf("type: %v", err)
	}

	zero := signPlace(t, priv, cfg, 1, domain.SideBuy)
	zero.Nonce = 0
	zero = sign(t, priv, cfg, zero)
	if _, err := svc.Admit(context.Background(), zero); !errors.Is(err, ErrNonce) {
		t.Fatalf("zero nonce: %v", err)
	}

	chain.next = map[string]uint64{string(valid.Owner): 3}
	stale := signPlace(t, priv, cfg, 2, domain.SideSell)
	if _, err := svc.Admit(context.Background(), stale); !errors.Is(err, ErrStaleNonce) {
		t.Fatalf("stale: %v", err)
	}
	if svc.NextPosition() != 1 {
		t.Fatalf("rejected command took a position: %d", svc.NextPosition())
	}

	chain.nonceErr = errors.New("connection refused")
	if _, err := svc.Admit(context.Background(), valid); !errors.Is(err, ErrChainUnavailable) {
		t.Fatalf("rpc: %v", err)
	}
	chain.nonceErr = nil
	chain.next = nil

	if _, err := svc.Admit(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), valid); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	second := signPlace(t, priv, cfg, 2, domain.SideBuy)
	if _, err := svc.Admit(context.Background(), second); err != nil {
		t.Fatalf("different command: %v", err)
	}
}

func TestAdmitConcurrentPositions(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	const n = 16
	cmds := make([]canonical.Command, n)
	for i := 0; i < n; i++ {
		cmds[i] = signPlace(t, newKey(), cfg, 1, domain.SideBuy)
	}
	positions := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, err := svc.Admit(context.Background(), cmds[i])
			if err != nil {
				t.Errorf("admit %d: %v", i, err)
				return
			}
			positions[i] = rec.Position
		}(i)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for _, pos := range positions {
		if pos == 0 || seen[pos] {
			t.Fatalf("positions %v", positions)
		}
		seen[pos] = true
	}
	for i := uint64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("missing %d in %v", i, positions)
		}
	}
	pending := svc.Pending()
	if len(pending) != n {
		t.Fatalf("pending %d", len(pending))
	}
	for i, rec := range pending {
		if rec.Position != uint64(i+1) || !rec.Provisional {
			t.Fatalf("pending[%d] %+v", i, rec)
		}
	}
}

func TestJournalReplayPreservesOrderAndSequence(t *testing.T) {
	cfg := testConfig(t)
	chain := &memChain{}
	svc := openTest(t, cfg, chain)
	var cmds []canonical.Command
	for i := 0; i < 3; i++ {
		cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
		cmds = append(cmds, cmd)
		if _, err := svc.Admit(context.Background(), cmd); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err := Open(cfg, chain)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	pending := svc.Pending()
	if len(pending) != 3 || svc.NextPosition() != 4 {
		t.Fatalf("pending %d next %d", len(pending), svc.NextPosition())
	}
	for i, rec := range pending {
		if rec.Position != uint64(i+1) || rec.Owner != sdk.AccAddress(cmds[i].Owner).String() {
			t.Fatalf("replay[%d] %+v", i, rec)
		}
	}
	if _, err := svc.Admit(context.Background(), cmds[0]); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate after restart: %v", err)
	}
}

func TestBatchSelectionAndSubmit(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxBatch = 2
	chain := &memChain{}
	var previous [32]byte
	for i := range previous {
		previous[i] = byte(i)
	}
	chain.head = Head{Latest: 7, Previous: previous, Revision: 11}
	svc := openTest(t, cfg, chain)
	var cmds []canonical.Command
	for i := 0; i < 3; i++ {
		cmd := signPlace(t, newKey(), cfg, 1, domain.SideBuy)
		cmds = append(cmds, cmd)
		if _, err := svc.Admit(context.Background(), cmd); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SubmitOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(chain.submitted) != 1 || len(chain.submitted[0].Commands) != 2 {
		t.Fatalf("submitted %#v", chain.submitted)
	}
	msg := chain.submitted[0]
	if msg.BatchNumber != 8 || msg.ExpectedExchangeRevision != 11 {
		t.Fatalf("batch %d revision %d", msg.BatchNumber, msg.ExpectedExchangeRevision)
	}
	if !bytes.Equal(msg.PreviousBatchCommitment, previous[:]) {
		t.Fatal("previous commitment")
	}
	if msg.Commands[0].Owner != sdk.AccAddress(cmds[0].Owner).String() || msg.Commands[1].Owner != sdk.AccAddress(cmds[1].Owner).String() {
		t.Fatal("batch did not keep admission order")
	}
	want, err := canonical.HashBatchID(8, 11, []canonical.Command{cmds[0], cmds[1]})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := commandsFromMsg(msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonical.HashBatchID(msg.BatchNumber, msg.ExpectedExchangeRevision, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("batch id mismatch")
	}
	st, ok := svc.CommandStatus(1)
	if !ok || st != StatusFinalized {
		t.Fatalf("status %s %v", st, ok)
	}
	pending := svc.Pending()
	if len(pending) != 1 || pending[0].Position != 3 {
		t.Fatalf("pending %+v", pending)
	}

	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err = Open(cfg, chain)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	pending = svc.Pending()
	if len(pending) != 1 || pending[0].Position != 3 {
		t.Fatalf("restart pending %+v", pending)
	}
	if st, ok := svc.CommandStatus(1); !ok || st != StatusFinalized {
		t.Fatalf("finalized reappeared as %s", st)
	}
	if svc.NextPosition() != 4 {
		t.Fatalf("next %d", svc.NextPosition())
	}
}

func TestSubmitFailureRestoresPending(t *testing.T) {
	cfg := testConfig(t)
	chain := &memChain{submitErr: errors.New("tx rejected")}
	svc := openTest(t, cfg, chain)
	first := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	second := signPlace(t, newKey(), cfg, 1, domain.SideBuy)
	if _, err := svc.Admit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	err := svc.SubmitOnce(context.Background())
	if !errors.Is(err, ErrBatchRejected) {
		t.Fatalf("got %v", err)
	}
	pending := svc.Pending()
	if len(pending) != 2 || pending[0].Position != 1 || pending[1].Position != 2 || pending[0].Status != StatusPending {
		t.Fatalf("pending %+v", pending)
	}
}

func TestStaleNonceRemovedDuringRecovery(t *testing.T) {
	cfg := testConfig(t)
	alice := newKey()
	bob := newKey()
	chain := &memChain{submitErr: errors.New("batch failed")}
	sell := signPlace(t, alice, cfg, 1, domain.SideSell)
	buy := signPlace(t, bob, cfg, 1, domain.SideBuy)
	chain.onSubmit = func() {
		if chain.next == nil {
			chain.next = map[string]uint64{}
		}
		chain.next[string(sell.Owner)] = 2
	}
	svc := openTest(t, cfg, chain)
	if _, err := svc.Admit(context.Background(), sell); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Admit(context.Background(), buy); err != nil {
		t.Fatal(err)
	}
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrBatchRejected) {
		t.Fatal(err)
	}
	pending := svc.Pending()
	if len(pending) != 1 || pending[0].Owner != sdk.AccAddress(buy.Owner).String() || pending[0].Position != 2 {
		t.Fatalf("pending %+v", pending)
	}
	if st, _ := svc.CommandStatus(1); st != StatusDropped {
		t.Fatalf("stale status %s", st)
	}
	if _, err := svc.Admit(context.Background(), sell); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("re-admit stale: %v", err)
	}
}

func TestHeadFailureRestoresPending(t *testing.T) {
	cfg := testConfig(t)
	chain := &memChain{}
	svc := openTest(t, cfg, chain)
	if _, err := svc.Admit(context.Background(), signPlace(t, newKey(), cfg, 1, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
	chain.headErr = errors.New("rpc down")
	if err := svc.SubmitOnce(context.Background()); !errors.Is(err, ErrChainUnavailable) {
		t.Fatalf("got %v", err)
	}
	if len(svc.Pending()) != 1 {
		t.Fatalf("pending %+v", svc.Pending())
	}
}

func TestPositionDoesNotWrap(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	svc.mu.Lock()
	svc.next = math.MaxUint64
	pos, err := svc.takePosition()
	if err != nil || pos != math.MaxUint64 {
		svc.mu.Unlock()
		t.Fatalf("pos %d err %v", pos, err)
	}
	if _, err := svc.takePosition(); !errors.Is(err, ErrSequenceExhausted) {
		svc.mu.Unlock()
		t.Fatal(err)
	}
	svc.mu.Unlock()
}

func TestHTTPAdmission(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health %d", res.StatusCode)
	}

	bad, err := http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader([]byte("{")))
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed %d", bad.StatusCode)
	}

	cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	res, err = http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(commandJSON(t, cmd)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body admitResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Accepted || !body.Provisional || body.SequencerPosition != 1 || body.Status != string(StatusPending) {
		t.Fatalf("body %+v", body)
	}

	dup, err := http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(commandJSON(t, cmd)))
	if err != nil {
		t.Fatal(err)
	}
	dup.Body.Close()
	if dup.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate %d", dup.StatusCode)
	}
}

func TestNormalizeNode(t *testing.T) {
	got, err := normalizeNode("tcp://127.0.0.1:26657")
	if err != nil || got != "http://127.0.0.1:26657" {
		t.Fatalf("%s %v", got, err)
	}
}

func commandJSON(t *testing.T, cmd canonical.Command) []byte {
	t.Helper()
	req := commandRequest{
		ProtocolVersion:    cmd.ProtocolVersion,
		ChainID:            cmd.ChainID,
		ExchangeInstanceID: string(cmd.ExchangeInstanceID),
		Owner:              sdk.AccAddress(cmd.Owner).String(),
		CommandNonce:       cmd.Nonce,
		PubKey:             hex.EncodeToString(cmd.PubKey),
		Signature:          hex.EncodeToString(cmd.Signature),
	}
	switch cmd.Type {
	case canonical.CommandTypePlace:
		req.CommandType = "place"
		req.Place = &placeRequest{
			MarketID:      uint64(cmd.Place.MarketID),
			Side:          sideName(cmd.Place.Side),
			OrderType:     "limit",
			TimeInForce:   "gtc",
			QuantityLots:  uint64(cmd.Place.Quantity),
			PriceTicks:    uint64(cmd.Place.Price),
			ExpiryHeight:  cmd.Place.ExpiryHeight,
			ClientOrderID: string(cmd.Place.ClientOrderID),
		}
	case canonical.CommandTypeCancel:
		req.CommandType = "cancel"
		req.Cancel = &cancelRequest{OrderID: hex.EncodeToString(cmd.Cancel.OrderID[:])}
	}
	bz, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return bz
}

func sideName(s domain.Side) string {
	if s == domain.SideSell {
		return "sell"
	}
	return "buy"
}
