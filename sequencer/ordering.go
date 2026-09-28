package sequencer

import (
	"bytes"
	"context"
	"errors"
	"math"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
)

// Admit validates cmd, assigns the next sequencer position, and journals it
// before reporting success. The position is the batch order.
func (s *Service) Admit(ctx context.Context, cmd canonical.Command) (Receipt, error) {
	if err := validate(s.cfg, cmd); err != nil {
		return Receipt{}, err
	}
	chainNext, err := s.chain.NextNonce(ctx, cmd.Owner)
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
	if err := s.journal.append(record{Position: pos, Status: statusPending, Command: encoded}); err != nil {
		s.rewind(pos)
		return Receipt{}, err
	}
	it := &item{position: pos, cmd: cmd, encoded: encoded, id: id}
	s.seen[id] = struct{}{}
	s.pending = append(s.pending, it)
	s.byPos[pos] = StatusPending
	return receiptFor(it, StatusPending), nil
}

func (s *Service) expectedNonce(owner []byte, chainNext uint64) (uint64, error) {
	expected := chainNext
	bump := func(n uint64) error {
		if n < expected {
			return nil
		}
		if n == math.MaxUint64 {
			return ErrNonce
		}
		expected = n + 1
		return nil
	}
	for _, it := range s.pending {
		if bytes.Equal(it.cmd.Owner, owner) {
			if err := bump(it.cmd.Nonce); err != nil {
				return 0, err
			}
		}
	}
	for _, it := range s.inflight {
		if bytes.Equal(it.cmd.Owner, owner) {
			if err := bump(it.cmd.Nonce); err != nil {
				return 0, err
			}
		}
	}
	return expected, nil
}
