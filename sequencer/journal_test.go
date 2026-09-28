package sequencer

import (
	"errors"
	"os"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestCodecRoundTrip(t *testing.T) {
	cfg := testConfig(t)
	priv := newKey()
	cmd := signPlace(t, priv, cfg, 4, domain.SideBuy)
	cmd.Place.ClientOrderID = []byte("desk-1")
	cmd = sign(t, priv, cfg, cmd)

	encoded, err := encodeSigned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeSigned(encoded)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encodeSigned(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(again) {
		t.Fatal("signed command encoding was not stable")
	}
}

func TestTruncatedJournal(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	if _, err := svc.Admit(t.Context(), signPlace(t, newKey(), cfg, 1, domain.SideBuy)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(cfg.JournalPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := Open(cfg, &memChain{}); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("got %v", err)
	}
}

func TestJournalGapAndBadStatus(t *testing.T) {
	path := testConfig(t).JournalPath
	j, _, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append(record{Position: 2, Status: statusPending, Command: []byte{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	j.Close()
	if _, _, err := openJournal(path); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("gap: %v", err)
	}

	cfg := testConfig(t)
	priv := newKey()
	cmd := signPlace(t, priv, cfg, 1, domain.SideSell)
	encoded, err := encodeSigned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	j, _, err = openJournal(cfg.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []byte{statusPending, statusFinalized, statusPending} {
		if err := j.append(record{Position: 1, Status: st, Command: encoded}); err != nil {
			t.Fatal(err)
		}
	}
	j.Close()
	if _, _, err := openJournal(cfg.JournalPath); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("status: %v", err)
	}
}

func TestInFlightReplayReturnsPending(t *testing.T) {
	cfg := testConfig(t)
	priv := newKey()
	cmd := signPlace(t, priv, cfg, 1, domain.SideBuy)
	svc := openTest(t, cfg, nil)
	if _, err := svc.Admit(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeSigned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := openJournal(cfg.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append(record{Position: 1, Status: statusInFlight, Command: encoded}); err != nil {
		t.Fatal(err)
	}
	j.Close()

	svc, err = Open(cfg, &memChain{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	pending := svc.Pending()
	if len(pending) != 1 || pending[0].Position != 1 || pending[0].Status != StatusPending {
		t.Fatalf("pending %+v", pending)
	}
	if svc.NextPosition() != 2 {
		t.Fatalf("next %d", svc.NextPosition())
	}
}
