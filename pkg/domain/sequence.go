package domain

import "math"

// NextSequence returns the next per-market order sequence.
// last is the last assigned sequence, or 0 when the market has none.
// Assigned sequences start at 1. Sequence 0 is not a resting-book position.
// Lower sequences are older and have price-time priority.
func NextSequence(last Sequence) (Sequence, error) {
	if last == math.MaxUint64 {
		return 0, ErrSequenceOverflow
	}
	return last + 1, nil
}

// ExpectedCommandNonce returns the only command nonce an account may use next.
// lastAccepted is 0 when the account has not had a command accepted.
// The first accepted nonce is 1. Callers compare the command nonce for equality
// with this value; nonces are never derived from wall-clock time.
func ExpectedCommandNonce(lastAccepted uint64) (uint64, error) {
	if lastAccepted == math.MaxUint64 {
		return 0, ErrNonceOverflow
	}
	return lastAccepted + 1, nil
}
