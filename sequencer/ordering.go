package sequencer

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
)

// Admit validates cmd, assigns the next sequencer position, and journals it
// before reporting success. The position is the batch order.
func (s *Service) Admit(ctx context.Context, cmd canonical.Command) (Receipt, error) {
	start := time.Now()
	kind := commandTypeLabel(cmd)
	s.met.countReceived(kind)
	rec, err := s.admit(ctx, cmd)
	s.met.observeAdmission(start, kind, err)
	return rec, err
}

func (s *Service) admit(ctx context.Context, cmd canonical.Command) (Receipt, error) {
	if err := validate(s.cfg, cmd); err != nil {
		return Receipt{}, err
	}
	chainNext, err := s.queryNonce(ctx, cmd.Owner)
	if err != nil {
		if errors.Is(err, ErrChainUnavailable) {
			return Receipt{}, err
		}
		return Receipt{}, errors.Join(ErrChainUnavailable, err)
	}
	if chainNext == 0 {
		return Receipt{}, ErrChainUnavailable
	}
	encoded, err := encodeSigned(cmd)
	if err != nil {
		return Receipt{}, err
	}
	id := commandID(encoded)
	cmd = cloneCommand(cmd)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[id]; ok {
		return Receipt{}, ErrDuplicate
	}
	if cmd.Nonce < chainNext {
		return Receipt{}, ErrStaleNonce
	}
	if len(s.pending)+len(s.inflight) >= s.cfg.MaxOutstanding {
		return Receipt{}, ErrQueueFull
	}
	if s.ownerLoad[string(cmd.Owner)] >= s.cfg.MaxPerOwner {
		return Receipt{}, ErrOwnerQueue
	}
	expected, err := s.expectedNonce(cmd.Owner, chainNext)
	if err != nil {
		return Receipt{}, err
	}
	if cmd.Nonce != expected {
		return Receipt{}, ErrNonce
	}
	pos, err := s.takePosition()
	if err != nil {
		return Receipt{}, err
	}
	if err := s.appendJournal([]record{{Position: pos, Status: statusPending, Command: encoded}}); err != nil {
		s.rewind(pos)
		s.syncGauges()
		return Receipt{}, err
	}
	it := &item{position: pos, cmd: cmd, encoded: encoded, id: id}
	s.seen[id] = struct{}{}
	s.track(it)
	s.ownerLoad[string(cmd.Owner)]++
	s.pending = append(s.pending, it)
	s.byPos[pos] = StatusPending
	s.syncGauges()
	return receiptFor(it, StatusPending), nil
}

func (s *Service) expectedNonce(owner []byte, chainNext uint64) (uint64, error) {
	set := s.outstanding[string(owner)]
	s.nonceLookups = len(set)
	if s.nonceLookups == 0 {
		s.nonceLookups = 1
	}
	expected := chainNext
	var high uint64
	var have bool
	for n := range set {
		if n < chainNext {
			continue
		}
		if !have || n > high {
			high = n
			have = true
		}
	}
	if !have {
		return expected, nil
	}
	if high == math.MaxUint64 {
		return 0, ErrNonce
	}
	return high + 1, nil
}

// LastNonceLookups is the number of this owner's queued nonces examined
// by the previous admission. It does not grow with other owners.
func (s *Service) LastNonceLookups() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nonceLookups
}
