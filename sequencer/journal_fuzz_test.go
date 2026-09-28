package sequencer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func FuzzJournalFrame(f *testing.F) {
	f.Add([]byte(journalMagic))
	f.Add([]byte{0, 0, 0, 1, 1})
	var buf bytes.Buffer
	_ = writeRecord(&buf, record{Position: 1, Status: statusPending, Command: []byte{9, 8, 7}})
	f.Add(buf.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := readRecord(bytes.NewReader(data))
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, ErrJournalCorrupt) {
			return
		}
		t.Fatalf("unexpected %v", err)
	})
}

func FuzzJournalRoundTrip(f *testing.F) {
	f.Add(uint64(1), byte(1), []byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, pos uint64, status byte, cmd []byte) {
		if pos == 0 || len(cmd) == 0 || len(cmd) > maxBlob-14 {
			return
		}
		rec := record{Position: pos, Status: status, Command: append([]byte(nil), cmd...)}
		var buf bytes.Buffer
		if err := writeRecord(&buf, rec); err != nil {
			t.Fatal(err)
		}
		got, err := readRecord(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.Position != pos || got.Status != status || !bytes.Equal(got.Command, cmd) {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestJournalBadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	if err := os.WriteFile(path, []byte("XXXX"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openJournal(path); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("got %v", err)
	}
}

func TestJournalBitFlip(t *testing.T) {
	cfg := testConfig(t)
	cmd := signPlace(t, newKey(), cfg, 1, 1)
	encoded, err := encodeSigned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := openJournal(cfg.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append(record{Position: 1, Status: statusPending, Command: encoded}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(cfg.JournalPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the frame length so replay cannot treat the record as intact.
	if _, err := f.WriteAt([]byte{0xff, 0xff, 0xff, 0xff}, int64(len(journalMagic))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := openJournal(cfg.JournalPath); !errors.Is(err, ErrJournalCorrupt) {
		t.Fatalf("got %v", err)
	}
}
