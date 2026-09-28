package sequencer

import "errors"

var (
	ErrUnsupportedVersion = errors.New("sequencer: unsupported protocol version")
	ErrUnsupportedCommand = errors.New("sequencer: unsupported command")
	ErrChainID            = errors.New("sequencer: chain id mismatch")
	ErrInstance           = errors.New("sequencer: exchange instance mismatch")
	ErrOwner              = errors.New("sequencer: owner missing")
	ErrPubKey             = errors.New("sequencer: unsupported public key")
	ErrSignature          = errors.New("sequencer: invalid signature")
	ErrPubKeyMismatch     = errors.New("sequencer: public key does not match owner")
	ErrNonce              = errors.New("sequencer: command nonce is not the next nonce")
	ErrStaleNonce         = errors.New("sequencer: command nonce is below the chain next nonce")
	ErrDuplicate          = errors.New("sequencer: duplicate command")
	ErrTooLarge           = errors.New("sequencer: command exceeds size limit")
	ErrMalformed          = errors.New("sequencer: malformed command")
	ErrSequenceExhausted  = errors.New("sequencer: sequence space exhausted")
	ErrJournalCorrupt     = errors.New("sequencer: journal is corrupt or truncated")
	ErrChainUnavailable   = errors.New("sequencer: chain unavailable")
	ErrBatchRejected      = errors.New("sequencer: batch transaction rejected")
	ErrEmptyBatch         = errors.New("sequencer: no pending commands")
	ErrHead               = errors.New("sequencer: invalid chain batch head")
	ErrQueueFull          = errors.New("sequencer: queue is full")
	ErrOwnerQueue         = errors.New("sequencer: owner queue is full")
)
