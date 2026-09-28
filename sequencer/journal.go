package sequencer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"syscall"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
)

const journalMagic = "OBJ2"
const journalRecordVersion byte = 2

const (
	statusPending     byte = 1
	statusInFlight    byte = 2
	statusFinalized   byte = 3
	statusDropped     byte = 4
	statusQuarantined byte = 5
)

// Status is the sequencer lifecycle of one admitted command.
type Status string

const (
	StatusPending     Status = "pending"
	StatusInFlight    Status = "in_flight"
	StatusFinalized   Status = "finalized"
	StatusDropped     Status = "dropped"
	StatusQuarantined Status = "quarantined"
)

type record struct {
	Position uint64
	Status   byte
	Command  []byte
}

type stored struct {
	position    uint64
	status      Status
	wasInFlight bool
	cmd         canonical.Command
	encoded     []byte
	id          [32]byte
}

type snapshot struct {
	next      uint64
	exhausted bool
	items     []stored
}

type Journal struct {
	f      *os.File
	failed error
}

func openJournal(path string) (*Journal, snapshot, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, snapshot{}, fmt.Errorf("sequencer: journal directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, snapshot{}, fmt.Errorf("sequencer: journal: %w", err)
	}
	j := &Journal{f: f}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, snapshot{}, fmt.Errorf("sequencer: journal is locked: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		j.Close()
		return nil, snapshot{}, fmt.Errorf("sequencer: journal: %w", err)
	}
	if info.Size() == 0 {
		if _, err := f.Write([]byte(journalMagic)); err != nil {
			j.Close()
			return nil, snapshot{}, fmt.Errorf("sequencer: journal: %w", err)
		}
		if err := f.Sync(); err != nil {
			j.Close()
			return nil, snapshot{}, fmt.Errorf("sequencer: journal: %w", err)
		}
		if err := syncDir(path); err != nil {
			j.Close()
			return nil, snapshot{}, err
		}
		return j, snapshot{next: 1}, nil
	}
	snap, err := j.replay()
	if err != nil {
		j.Close()
		return nil, snapshot{}, err
	}
	return j, snap, nil
}

func (j *Journal) Close() error {
	if j == nil || j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

func (j *Journal) append(rec record) error {
	return j.appendMany([]record{rec})
}

func (j *Journal) appendMany(recs []record) error {
	if j.failed != nil {
		return j.failed
	}
	var buf bytes.Buffer
	for _, rec := range recs {
		if err := writeRecord(&buf, rec); err != nil {
			return err
		}
	}
	if _, err := j.f.Write(buf.Bytes()); err != nil {
		j.failed = fmt.Errorf("sequencer: journal write: %w", err)
		return j.failed
	}
	if err := j.f.Sync(); err != nil {
		j.failed = fmt.Errorf("sequencer: journal sync: %w", err)
		return j.failed
	}
	return nil
}

func (j *Journal) replay() (snapshot, error) {
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return snapshot{}, fmt.Errorf("sequencer: journal: %w", err)
	}
	magic := make([]byte, len(journalMagic))
	if _, err := io.ReadFull(j.f, magic); err != nil {
		return snapshot{}, fmt.Errorf("%w: header: %v", ErrJournalCorrupt, err)
	}
	if string(magic) != journalMagic {
		return snapshot{}, fmt.Errorf("%w: unsupported journal header %q", ErrJournalCorrupt, magic)
	}
	byPos := make(map[uint64]*stored)
	var order []uint64
	var next uint64 = 1
	var exhausted bool
	for {
		rec, err := readRecord(j.f)
		if err == io.EOF {
			break
		}
		if err != nil {
			return snapshot{}, err
		}
		st, err := statusFromByte(rec.Status)
		if err != nil {
			return snapshot{}, err
		}
		cur, ok := byPos[rec.Position]
		if !ok {
			if exhausted || rec.Position != next {
				return snapshot{}, fmt.Errorf("%w: position %d", ErrJournalCorrupt, rec.Position)
			}
			if st != StatusPending {
				return snapshot{}, fmt.Errorf("%w: position %d does not start pending", ErrJournalCorrupt, rec.Position)
			}
			cmd, err := decodeSigned(rec.Command)
			if err != nil {
				return snapshot{}, err
			}
			item := &stored{
				position: rec.Position,
				status:   st,
				cmd:      cmd,
				encoded:  append([]byte(nil), rec.Command...),
				id:       commandID(rec.Command),
			}
			byPos[rec.Position] = item
			order = append(order, rec.Position)
			if rec.Position == math.MaxUint64 {
				exhausted = true
			} else {
				next = rec.Position + 1
			}
			continue
		}
		if !bytes.Equal(cur.encoded, rec.Command) {
			return snapshot{}, fmt.Errorf("%w: position %d command changed", ErrJournalCorrupt, rec.Position)
		}
		if !canTransition(cur.status, st) {
			return snapshot{}, fmt.Errorf("%w: position %d status", ErrJournalCorrupt, rec.Position)
		}
		if st == StatusInFlight {
			cur.wasInFlight = true
		}
		if st == StatusPending {
			cur.wasInFlight = false
		}
		cur.status = st
	}
	if _, err := j.f.Seek(0, io.SeekEnd); err != nil {
		return snapshot{}, fmt.Errorf("sequencer: journal: %w", err)
	}
	items := make([]stored, 0, len(order))
	for _, pos := range order {
		items = append(items, *byPos[pos])
	}
	return snapshot{next: next, exhausted: exhausted, items: items}, nil
}

func canTransition(from, to Status) bool {
	if from == to {
		return false
	}
	switch from {
	case StatusPending:
		return to == StatusInFlight || to == StatusFinalized || to == StatusDropped || to == StatusQuarantined
	case StatusInFlight:
		return to == StatusPending || to == StatusFinalized || to == StatusDropped || to == StatusQuarantined
	default:
		return false
	}
}

func statusFromByte(v byte) (Status, error) {
	switch v {
	case statusPending:
		return StatusPending, nil
	case statusInFlight:
		return StatusInFlight, nil
	case statusFinalized:
		return StatusFinalized, nil
	case statusDropped:
		return StatusDropped, nil
	case statusQuarantined:
		return StatusQuarantined, nil
	default:
		return "", fmt.Errorf("%w: status %d", ErrJournalCorrupt, v)
	}
}

func statusByte(s Status) (byte, error) {
	switch s {
	case StatusPending:
		return statusPending, nil
	case StatusInFlight:
		return statusInFlight, nil
	case StatusFinalized:
		return statusFinalized, nil
	case StatusDropped:
		return statusDropped, nil
	case StatusQuarantined:
		return statusQuarantined, nil
	default:
		return 0, fmt.Errorf("%w: status %s", ErrJournalCorrupt, s)
	}
}

func writeRecord(w io.Writer, rec record) error {
	if rec.Position == 0 || len(rec.Command) == 0 || len(rec.Command) > maxBlob {
		return fmt.Errorf("%w: record", ErrJournalCorrupt)
	}
	body := recordBody(rec)
	sum := crc32.ChecksumIEEE(body)
	payload := make([]byte, len(body)+4)
	copy(payload, body)
	binary.BigEndian.PutUint32(payload[len(body):], sum)
	var frame [4]byte
	binary.BigEndian.PutUint32(frame[:], uint32(len(payload)))
	if _, err := w.Write(frame[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func recordBody(rec record) []byte {
	body := make([]byte, 0, 14+len(rec.Command))
	body = append(body, journalRecordVersion)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], rec.Position)
	body = append(body, num[:]...)
	body = append(body, rec.Status)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(rec.Command)))
	body = append(body, n[:]...)
	body = append(body, rec.Command...)
	return body
}

func readRecord(r io.Reader) (record, error) {
	var frame [4]byte
	if _, err := io.ReadFull(r, frame[:]); err != nil {
		if err == io.EOF {
			return record{}, io.EOF
		}
		return record{}, fmt.Errorf("%w: %v", ErrJournalCorrupt, err)
	}
	n := binary.BigEndian.Uint32(frame[:])
	if n < 18 || n > maxBlob {
		return record{}, fmt.Errorf("%w: record length", ErrJournalCorrupt)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return record{}, fmt.Errorf("%w: %v", ErrJournalCorrupt, err)
	}
	body, sumBytes := payload[:len(payload)-4], payload[len(payload)-4:]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(sumBytes) {
		return record{}, fmt.Errorf("%w: record checksum", ErrJournalCorrupt)
	}
	if body[0] != journalRecordVersion {
		return record{}, fmt.Errorf("%w: record version", ErrJournalCorrupt)
	}
	pos := binary.BigEndian.Uint64(body[1:9])
	status := body[9]
	cmdLen := binary.BigEndian.Uint32(body[10:14])
	if int(cmdLen) != len(body)-14 {
		return record{}, fmt.Errorf("%w: command length", ErrJournalCorrupt)
	}
	return record{Position: pos, Status: status, Command: append([]byte(nil), body[14:]...)}, nil
}

func syncDir(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("sequencer: journal directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sequencer: journal directory sync: %w", err)
	}
	return nil
}
