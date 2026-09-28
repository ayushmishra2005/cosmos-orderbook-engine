package sequencer

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestJournalChecksumRejectsCorruption(t *testing.T) {
	cfg := testConfig(t)
	encoded := mustEncode(t, cfg)
	path := cfg.JournalPath
	j, _, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append(record{Position: 1, Status: statusPending, Command: encoded}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// OBJ2 | u32 len | version | position | status | cmdlen | command | crc32
	offsets := map[string]int{
		"status":   4 + 4 + 1 + 8,
		"position": 4 + 4 + 1 + 7,
		"payload":  len(original) - 5,
		"checksum": len(original) - 1,
		"length":   4,
	}
	for name, at := range offsets {
		corruptJournal(t, path, original, at)
		if _, _, err := openJournal(path); !errors.Is(err, ErrJournalCorrupt) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestOldJournalHeaderIsRejected(t *testing.T) {
	path := testConfig(t).JournalPath
	if err := os.WriteFile(path, []byte("OBJ1junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := openJournal(path)
	if !errors.Is(err, ErrJournalCorrupt) || !strings.Contains(err.Error(), "unsupported journal header") {
		t.Fatal(err)
	}
}

func mustEncode(t *testing.T, cfg Config) []byte {
	t.Helper()
	encoded, err := encodeSigned(signPlace(t, newKey(), cfg, 1, domain.SideBuy))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func corruptJournal(t *testing.T, path string, original []byte, at int) {
	t.Helper()
	bz := append([]byte(nil), original...)
	bz[at] ^= 0x01
	if err := os.WriteFile(path, bz, 0o600); err != nil {
		t.Fatal(err)
	}
}
