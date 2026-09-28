package types

import (
	"encoding/binary"
	"errors"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const (
	ModuleName = "batch"
	StoreKey   = ModuleName
)

const (
	// Prefixes belong to the batch module store. They are independent of
	// x/exchange. Zero is unused.
	PrefixParams  byte = 0x01
	PrefixBatch   byte = 0x02
	PrefixBatchID byte = 0x03
)

const (
	CommandPlace  byte = 1
	CommandCancel byte = 2

	StatusResting   byte = 1
	StatusFilled    byte = 2
	StatusCancelled byte = 3
	StatusUnfilled  byte = 4

	// MaxCommands bounds one finalized batch.
	MaxCommands = 128
)

const (
	EventTypeBatchFinalized = "batch_finalized"
)

var (
	ErrUnauthorized       = errors.New("batch: unauthorized submitter")
	ErrBatchNumber        = errors.New("batch: unexpected batch number")
	ErrStaleRevision      = errors.New("batch: stale exchange revision")
	ErrInvalidSignature   = errors.New("batch: invalid signature")
	ErrPubKey             = errors.New("batch: unsupported public key")
	ErrPubKeyMismatch     = errors.New("batch: public key does not match owner")
	ErrChainID            = errors.New("batch: chain id mismatch")
	ErrInstance           = errors.New("batch: exchange instance mismatch")
	ErrEmpty              = errors.New("batch: empty batch")
	ErrLimit              = errors.New("batch: too many commands")
	ErrNotFound           = errors.New("batch: not found")
	ErrCorrupt            = errors.New("batch: corrupt state")
	ErrNegativeHeight     = errors.New("batch: negative block height")
	ErrInvariant          = errors.New("batch: revision invariant failed")
	ErrPreviousCommitment = errors.New("batch: previous commitment mismatch")
)

// BatchID identifies one ordered batch. It is not an exchange state root.
type BatchID [32]byte

// IsZero reports whether the identifier is all zeros.
func (id BatchID) IsZero() bool { return id == BatchID{} }

// ResultsHash is the digest of the ordered command results.
// It is not an exchange state root.
type ResultsHash [32]byte

// IsZero reports whether the digest is all zeros.
func (h ResultsHash) IsZero() bool { return h == ResultsHash{} }

// BatchCommitment binds one finalized batch to the previous commitment and its results hash.
// The zero value is the genesis previous commitment. It is not an exchange state root.
type BatchCommitment [32]byte

// IsZero reports whether the commitment is all zeros.
func (c BatchCommitment) IsZero() bool { return c == BatchCommitment{} }

// Params is the batch head: who may submit, the latest number, and the latest commitment.
// Head is 32 zero bytes before batch 1.
type Params struct {
	Submitter []byte
	Latest    uint64
	Head      BatchCommitment
}

// TradeRef identifies one fill by market and trade sequence.
type TradeRef struct {
	MarketID domain.MarketID
	Sequence uint64
}

// CommandResult is the stored summary of one successful command.
type CommandResult struct {
	Index     uint32
	Type      byte
	Owner     []byte
	OrderID   domain.OrderID
	Status    byte
	Remaining uint64
	Trades    []TradeRef
}

// Batch is one finalized batch. It does not copy the order book.
type Batch struct {
	Number       uint64
	ID           BatchID
	Height       uint64
	PreRevision  uint64
	PostRevision uint64
	Previous     BatchCommitment
	Commitment   BatchCommitment
	ResultsHash  ResultsHash
	Results      []CommandResult
}

// Validate checks a batch record that is about to be stored or imported.
func (b Batch) Validate() error {
	if b.Number == 0 || b.ID.IsZero() {
		return ErrCorrupt
	}
	if b.Number == 1 && !b.Previous.IsZero() {
		return ErrPreviousCommitment
	}
	n := len(b.Results)
	if n == 0 || n > MaxCommands {
		return ErrCorrupt
	}
	want, err := arithmetic.Add(b.PreRevision, uint64(n))
	if err != nil || want != b.PostRevision {
		return ErrInvariant
	}
	for i := range b.Results {
		if int(b.Results[i].Index) != i {
			return ErrCorrupt
		}
		if err := b.Results[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks one command summary.
func (r CommandResult) Validate() error {
	if r.Type != CommandPlace && r.Type != CommandCancel {
		return ErrCorrupt
	}
	if err := domain.ValidateOwner(r.Owner); err != nil {
		return ErrCorrupt
	}
	if r.OrderID.IsZero() {
		return ErrCorrupt
	}
	switch r.Status {
	case StatusResting:
		if r.Type != CommandPlace || r.Remaining == 0 {
			return ErrCorrupt
		}
	case StatusFilled:
		if r.Type != CommandPlace || r.Remaining != 0 || len(r.Trades) == 0 {
			return ErrCorrupt
		}
	case StatusUnfilled:
		if r.Type != CommandPlace || r.Remaining == 0 {
			return ErrCorrupt
		}
	case StatusCancelled:
		if r.Type != CommandCancel || r.Remaining != 0 || len(r.Trades) != 0 {
			return ErrCorrupt
		}
	default:
		return ErrCorrupt
	}
	for _, trade := range r.Trades {
		if trade.MarketID == 0 || trade.Sequence == 0 {
			return ErrCorrupt
		}
	}
	return nil
}

// ParamsKey is the singleton head.
func ParamsKey() []byte { return []byte{PrefixParams} }

// BatchKey is the record key for one batch number.
func BatchKey(number uint64) []byte {
	var key [9]byte
	key[0] = PrefixBatch
	binary.BigEndian.PutUint64(key[1:], number)
	return key[:]
}

// BatchIDKey indexes a batch ID to its number.
func BatchIDKey(id BatchID) []byte {
	key := make([]byte, 1+len(id))
	key[0] = PrefixBatchID
	copy(key[1:], id[:])
	return key
}
