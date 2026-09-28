package sequencer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

// Config is the sequencer process configuration.
// Remote callers cannot change it.
type Config struct {
	ChainID         string
	InstanceID      []byte
	Submitter       string
	JournalPath     string
	MaxCommandBytes int
	MaxBatch        int
	BatchInterval   time.Duration
	RetryInitial    time.Duration
	RetryMax        time.Duration
	Logger          *slog.Logger
}

func (c Config) normalized() (Config, error) {
	if c.MaxCommandBytes == 0 {
		c.MaxCommandBytes = 8192
	}
	if c.MaxBatch == 0 {
		c.MaxBatch = 100
	}
	if c.BatchInterval == 0 {
		c.BatchInterval = 2 * time.Second
	}
	if c.RetryInitial == 0 {
		c.RetryInitial = time.Second
	}
	if c.RetryMax == 0 {
		c.RetryMax = 30 * time.Second
	}
	if c.ChainID == "" {
		return Config{}, fmt.Errorf("sequencer: chain id is required")
	}
	if len(c.InstanceID) == 0 {
		return Config{}, fmt.Errorf("sequencer: exchange instance is required")
	}
	if c.JournalPath == "" {
		return Config{}, fmt.Errorf("sequencer: journal path is required")
	}
	if _, err := sdk.AccAddressFromBech32(c.Submitter); err != nil {
		return Config{}, fmt.Errorf("sequencer: submitter: %w", err)
	}
	if c.MaxBatch < 1 || c.MaxBatch > batchtypes.MaxCommands {
		return Config{}, fmt.Errorf("sequencer: max batch commands must be 1..%d", batchtypes.MaxCommands)
	}
	if c.MaxCommandBytes < 1 {
		return Config{}, fmt.Errorf("sequencer: max command bytes must be positive")
	}
	if c.BatchInterval < 0 {
		return Config{}, fmt.Errorf("sequencer: batch interval must be positive")
	}
	if c.RetryInitial < 0 || c.RetryMax < 0 {
		return Config{}, fmt.Errorf("sequencer: retry delay must be positive")
	}
	if c.RetryMax < c.RetryInitial {
		c.RetryMax = c.RetryInitial
	}
	c.InstanceID = append([]byte(nil), c.InstanceID...)
	return c, nil
}

// Receipt is a provisional admission result.
// Provisional remains true until the chain finalizes the command.
type Receipt struct {
	Position     uint64 `json:"sequencer_position"`
	Status       Status `json:"status"`
	Provisional  bool   `json:"provisional"`
	Owner        string `json:"owner,omitempty"`
	CommandNonce uint64 `json:"command_nonce,omitempty"`
	CommandType  string `json:"command_type,omitempty"`
}

type item struct {
	position uint64
	cmd      canonical.Command
	encoded  []byte
	id       [32]byte
}

// Service admits commands, journals them, and submits batches.
type Service struct {
	cfg     Config
	chain   Chain
	journal *Journal
	log     *slog.Logger

	mu             sync.Mutex
	submitMu       sync.Mutex
	next           uint64
	exhausted      bool
	pending        []*item
	inflight       []*item
	seen           map[[32]byte]struct{}
	byPos          map[uint64]Status
	backoff        time.Duration
	chainKnown     bool
	chainConnected bool
	latestBatch    uint64
	met            *metrics
}

// Open replays the journal and restores the next sequence number.
// In-flight commands from a crashed submit are returned to pending.
func Open(cfg Config, chain Chain) (*Service, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if chain == nil {
		return nil, fmt.Errorf("sequencer: chain client is required")
	}
	j, snap, err := openJournal(cfg.JournalPath)
	if err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		cfg:       cfg,
		chain:     chain,
		journal:   j,
		log:       log,
		next:      snap.next,
		exhausted: snap.exhausted,
		seen:      make(map[[32]byte]struct{}, len(snap.items)),
		byPos:     make(map[uint64]Status, len(snap.items)),
		met:       newMetrics(),
	}
	var fix []record
	for _, it := range snap.items {
		if err := validate(cfg, it.cmd); err != nil {
			j.Close()
			return nil, fmt.Errorf("%w: position %d: %v", ErrJournalCorrupt, it.position, err)
		}
		if _, ok := s.seen[it.id]; ok {
			j.Close()
			return nil, fmt.Errorf("%w: duplicate command at position %d", ErrJournalCorrupt, it.position)
		}
		s.seen[it.id] = struct{}{}
		st := it.status
		if st == StatusInFlight {
			st = StatusPending
			fix = append(fix, record{Position: it.position, Status: statusPending, Command: it.encoded})
		}
		s.byPos[it.position] = st
		if st == StatusPending {
			s.pending = append(s.pending, &item{
				position: it.position,
				cmd:      cloneCommand(it.cmd),
				encoded:  append([]byte(nil), it.encoded...),
				id:       it.id,
			})
		}
	}
	if len(fix) > 0 {
		if err := j.appendMany(fix); err != nil {
			j.Close()
			return nil, err
		}
	}
	if s.next == 0 && !s.exhausted {
		s.next = 1
	}
	s.syncGauges()
	return s, nil
}

// Close releases the journal lock.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.journal.Close()
}

// NextPosition is the next sequencer position that will be assigned.
// It is 0 when the sequence space is exhausted.
func (s *Service) NextPosition() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exhausted {
		return 0
	}
	return s.next
}

// CommandStatus reports the latest journaled status of a position.
func (s *Service) CommandStatus(position uint64) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byPos[position]
	return st, ok
}

// Pending returns admitted commands that are not finalized, oldest first.
func (s *Service) Pending() []Receipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Receipt, 0, len(s.inflight)+len(s.pending))
	for _, it := range s.inflight {
		out = append(out, receiptFor(it, StatusInFlight))
	}
	for _, it := range s.pending {
		out = append(out, receiptFor(it, StatusPending))
	}
	return out
}

func receiptFor(it *item, st Status) Receipt {
	kind := "place"
	if it.cmd.Type == canonical.CommandTypeCancel {
		kind = "cancel"
	}
	return Receipt{
		Position:     it.position,
		Status:       st,
		Provisional:  st != StatusFinalized,
		Owner:        sdk.AccAddress(it.cmd.Owner).String(),
		CommandNonce: it.cmd.Nonce,
		CommandType:  kind,
	}
}

// Run submits one batch per interval until ctx is cancelled.
// After a transport or rejection error it waits with exponential backoff.
// The wait does not change command order.
func (s *Service) Run(ctx context.Context) error {
	timer := time.NewTimer(s.cfg.BatchInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		err := s.SubmitOnce(ctx)
		wait := s.cfg.BatchInterval
		if err != nil && !errors.Is(err, ErrEmptyBatch) {
			s.log.Error("batch submission", "err", err)
			wait = s.failureDelay()
		}
		timer.Reset(wait)
	}
}

// SubmitOnce builds and submits the oldest pending commands.
func (s *Service) SubmitOnce(ctx context.Context) error {
	err := s.submitOnce(ctx)
	if err != nil && !errors.Is(err, ErrEmptyBatch) {
		s.met.countBatchFailed()
	}
	return err
}

func (s *Service) submitOnce(ctx context.Context) error {
	if !s.submitMu.TryLock() {
		return nil
	}
	defer s.submitMu.Unlock()

	s.mu.Lock()
	if len(s.inflight) > 0 {
		batch := append([]*item(nil), s.inflight...)
		s.mu.Unlock()
		return s.recover(ctx, batch, errors.New("previous batch was still in flight"))
	}
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return ErrEmptyBatch
	}
	n := s.cfg.MaxBatch
	if n > len(s.pending) {
		n = len(s.pending)
	}
	batch := make([]*item, n)
	copy(batch, s.pending[:n])
	s.pending = append([]*item(nil), s.pending[n:]...)
	s.inflight = batch
	recs := make([]record, len(batch))
	for i, it := range batch {
		recs[i] = record{Position: it.position, Status: statusInFlight, Command: it.encoded}
		s.byPos[it.position] = StatusInFlight
	}
	if err := s.appendJournal(recs); err != nil {
		s.pending = append(batch, s.pending...)
		s.inflight = nil
		for _, it := range batch {
			s.byPos[it.position] = StatusPending
		}
		s.syncGauges()
		s.mu.Unlock()
		return err
	}
	s.syncGauges()
	cmds := make([]canonical.Command, len(batch))
	for i, it := range batch {
		cmds[i] = cloneCommand(it.cmd)
	}
	s.mu.Unlock()

	head, err := s.queryHead(ctx)
	if err != nil {
		if rerr := s.restore(batch); rerr != nil {
			return rerr
		}
		return fmt.Errorf("%w: %v", ErrChainUnavailable, err)
	}
	msg, _, err := BuildFinalize(s.cfg.Submitter, head, cmds)
	if err != nil {
		if rerr := s.restore(batch); rerr != nil {
			return rerr
		}
		return err
	}
	submitStart := time.Now()
	err = s.chain.Submit(ctx, msg)
	s.met.observeSubmit(submitStart, len(batch))
	if err != nil {
		return s.recover(ctx, batch, err)
	}
	if err := s.mark(batch, StatusFinalized, false); err != nil {
		s.log.Error("chain accepted the batch but the journal update failed", "err", err, "batch", msg.BatchNumber)
		return err
	}
	s.met.countFinalized(len(batch), msg.BatchNumber)
	s.noteFinalized(msg.BatchNumber)
	s.resetBackoff()
	s.log.Info("batch finalized", "batch", msg.BatchNumber, "commands", len(batch), "first", batch[0].position, "last", batch[len(batch)-1].position)
	return nil
}

func (s *Service) restore(batch []*item) error {
	return s.mark(batch, StatusPending, true)
}

func (s *Service) recover(ctx context.Context, batch []*item, cause error) error {
	// Drop a command only when its nonce is already behind the chain.
	// An underfunded order stays pending. A later deposit can make it valid.
	// The caller waits out the retry delay before broadcasting again.
	type decision struct {
		it   *item
		drop bool
	}
	decisions := make([]decision, len(batch))
	for i, it := range batch {
		next, err := s.queryNonce(ctx, it.cmd.Owner)
		if err != nil {
			if rerr := s.restore(batch); rerr != nil {
				return rerr
			}
			return fmt.Errorf("%w: %v", ErrChainUnavailable, err)
		}
		decisions[i] = decision{it: it, drop: it.cmd.Nonce < next}
	}
	recs := make([]record, len(decisions))
	var keep []*item
	for i, d := range decisions {
		st := statusPending
		if d.drop {
			st = statusDropped
		} else {
			keep = append(keep, d.it)
		}
		recs[i] = record{Position: d.it.position, Status: st, Command: d.it.encoded}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendJournal(recs); err != nil {
		return err
	}
	for _, d := range decisions {
		if d.drop {
			s.byPos[d.it.position] = StatusDropped
		} else {
			s.byPos[d.it.position] = StatusPending
		}
	}
	s.inflight = nil
	s.pending = append(keep, s.pending...)
	s.syncGauges()
	return fmt.Errorf("%w: %v", ErrBatchRejected, cause)
}

func (s *Service) mark(batch []*item, status Status, prepend bool) error {
	b, err := statusByte(status)
	if err != nil {
		return err
	}
	recs := make([]record, len(batch))
	for i, it := range batch {
		recs[i] = record{Position: it.position, Status: b, Command: it.encoded}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendJournal(recs); err != nil {
		return err
	}
	for _, it := range batch {
		s.byPos[it.position] = status
	}
	s.inflight = nil
	if prepend {
		s.pending = append(append([]*item(nil), batch...), s.pending...)
	}
	s.syncGauges()
	return nil
}

func (s *Service) takePosition() (uint64, error) {
	if s.exhausted || s.next == 0 {
		return 0, ErrSequenceExhausted
	}
	pos := s.next
	if pos == math.MaxUint64 {
		s.exhausted = true
		s.next = 0
		return pos, nil
	}
	s.next++
	return pos, nil
}

func (s *Service) appendJournal(recs []record) error {
	start := time.Now()
	err := s.journal.appendMany(recs)
	s.met.observeJournal(start)
	return err
}

func (s *Service) queryHead(ctx context.Context) (Head, error) {
	start := time.Now()
	head, err := s.chain.Head(ctx)
	s.met.observeChain(start)
	s.noteChain(err == nil, head.Latest, err == nil)
	return head, err
}

func (s *Service) queryNonce(ctx context.Context, owner []byte) (uint64, error) {
	start := time.Now()
	next, err := s.chain.NextNonce(ctx, owner)
	s.met.observeChain(start)
	s.noteChain(err == nil, 0, false)
	return next, err
}

func (s *Service) noteChain(ok bool, latest uint64, haveLatest bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chainKnown = true
	s.chainConnected = ok
	s.met.setChainUp(ok)
	if ok && haveLatest {
		s.latestBatch = latest
		s.met.setLastBatch(latest)
	}
}

func (s *Service) noteFinalized(batch uint64) {
	s.mu.Lock()
	s.latestBatch = batch
	s.mu.Unlock()
}

// syncGauges reads sequencer queues. The caller holds s.mu, or Open has not returned.
func (s *Service) syncGauges() {
	next := s.next
	if s.exhausted {
		next = 0
	}
	s.met.setGauges(len(s.pending), len(s.inflight), next)
}

func (s *Service) failureDelay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.backoff
	if d == 0 {
		d = s.cfg.RetryInitial
	}
	next := d * 2
	if next <= 0 || next > s.cfg.RetryMax {
		next = s.cfg.RetryMax
	}
	s.backoff = next
	s.met.setBackoff(d)
	return d
}

func (s *Service) resetBackoff() {
	s.mu.Lock()
	s.backoff = 0
	s.met.setBackoff(0)
	s.mu.Unlock()
}

func (s *Service) rewind(pos uint64) {
	if pos == math.MaxUint64 {
		s.exhausted = false
		s.next = math.MaxUint64
		return
	}
	if s.next == pos+1 {
		s.next = pos
	}
}
