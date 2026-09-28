package canonical

import (
	"crypto/sha256"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const (
	// BatchResultsDomain separates a results hash from a batch ID and a commitment.
	BatchResultsDomain = "cosmos-orderbook/batch-results/v1"
	// BatchCommitmentDomain separates a batch commitment from a results hash.
	BatchCommitmentDomain = "cosmos-orderbook/batch-commitment/v1"
	// BatchCommitmentVersion is the only commitment version this encoder accepts.
	BatchCommitmentVersion uint32 = 1
)

// Result status bytes match the batch command-result statuses.
const (
	ResultResting   uint8 = 1
	ResultFilled    uint8 = 2
	ResultCancelled uint8 = 3
	ResultUnfilled  uint8 = 4
)

// GenesisBatchCommitment is the previous commitment required by batch 1.
// It is 32 zero bytes. It is not derived from the chain, the clock, or the host.
var GenesisBatchCommitment [32]byte

// ResultTrade identifies one fill by market and trade sequence.
type ResultTrade struct {
	MarketID domain.MarketID
	Sequence uint64
}

// Result is one command outcome in the ResultsHash preimage.
// Trades stay in execution order.
type Result struct {
	Index     uint32
	Type      CommandType
	Owner     []byte
	OrderID   domain.OrderID
	Status    uint8
	Remaining uint64
	Trades    []ResultTrade
}

// BatchCommitmentInput is the preimage of one batch commitment.
// PreviousBatchCommitment is 32 zero bytes for batch 1.
type BatchCommitmentInput struct {
	Version                 uint32
	ChainID                 string
	ExchangeInstanceID      []byte
	BatchNumber             uint64
	BatchID                 [32]byte
	PreviousBatchCommitment [32]byte
	ExecutionHeight         uint64
	PreExchangeRevision     uint64
	PostExchangeRevision    uint64
	ResultsHash             [32]byte
}

// EncodeCommandResult encodes one result.
//
//	u32be index
//	u8 commandType
//	u64be len(owner) | owner
//	order ID
//	u8 status
//	u64be remaining
//	u64be tradeCount
//	for each trade, in order:
//	  u64be marketID
//	  u64be sequence
func EncodeCommandResult(r Result) ([]byte, error) {
	if r.Type != CommandTypePlace && r.Type != CommandTypeCancel {
		return nil, ErrInvalidResult
	}
	if err := domain.ValidateOwner(r.Owner); err != nil {
		return nil, err
	}
	if r.OrderID.IsZero() {
		return nil, domain.ErrInvalidOrderID
	}
	switch r.Status {
	case ResultResting, ResultFilled, ResultCancelled, ResultUnfilled:
	default:
		return nil, ErrInvalidResult
	}
	for _, trade := range r.Trades {
		if trade.MarketID == 0 || trade.Sequence == 0 {
			return nil, ErrInvalidResult
		}
	}
	dst := make([]byte, 0, 64+len(r.Owner)+16*len(r.Trades))
	dst = appendU32(dst, r.Index)
	dst = append(dst, byte(r.Type))
	dst = appendLenPrefixed(dst, r.Owner)
	dst = append(dst, r.OrderID[:]...)
	dst = append(dst, r.Status)
	dst = appendU64(dst, r.Remaining)
	dst = appendU64(dst, uint64(len(r.Trades)))
	for _, trade := range r.Trades {
		dst = appendU64(dst, uint64(trade.MarketID))
		dst = appendU64(dst, trade.Sequence)
	}
	return dst, nil
}

// HashResults is SHA-256 over the ordered command results.
// The digest is not an exchange state root.
//
//	u64be len(domain) | domain
//	u64be resultCount
//	for each result, in order:
//	  u64be len(result) | result
func HashResults(results []Result) ([32]byte, error) {
	if len(results) == 0 {
		return [32]byte{}, ErrInvalidResult
	}
	dst := make([]byte, 0, 64)
	dst = appendLenPrefixed(dst, []byte(BatchResultsDomain))
	dst = appendU64(dst, uint64(len(results)))
	for i := range results {
		body, err := EncodeCommandResult(results[i])
		if err != nil {
			return [32]byte{}, err
		}
		dst = appendLenPrefixed(dst, body)
	}
	return sha256.Sum256(dst), nil
}

// HashBatchCommitment is SHA-256 over one finalized batch header.
// It binds BatchID, the previous commitment, and ResultsHash.
// It is not an exchange state root.
//
//	u64be len(domain) | domain
//	u32be version
//	u64be len(chainID) | chainID
//	u64be len(instance) | instance
//	u64be batchNumber
//	batch ID
//	previous batch commitment
//	u64be executionHeight
//	u64be preExchangeRevision
//	u64be postExchangeRevision
//	results hash
func HashBatchCommitment(in BatchCommitmentInput) ([32]byte, error) {
	if in.Version != BatchCommitmentVersion {
		return [32]byte{}, ErrUnsupportedVersion
	}
	if in.ChainID == "" || len(in.ChainID) > maxChainIDLen {
		return [32]byte{}, ErrInvalidChainID
	}
	if len(in.ExchangeInstanceID) == 0 || len(in.ExchangeInstanceID) > maxInstanceIDLen {
		return [32]byte{}, ErrInvalidInstanceID
	}
	if in.BatchNumber == 0 || in.BatchID == [32]byte{} {
		return [32]byte{}, ErrInvalidCommitment
	}
	dst := make([]byte, 0, 256+len(in.ChainID)+len(in.ExchangeInstanceID))
	dst = appendLenPrefixed(dst, []byte(BatchCommitmentDomain))
	dst = appendU32(dst, in.Version)
	dst = appendLenPrefixed(dst, []byte(in.ChainID))
	dst = appendLenPrefixed(dst, in.ExchangeInstanceID)
	dst = appendU64(dst, in.BatchNumber)
	dst = append(dst, in.BatchID[:]...)
	dst = append(dst, in.PreviousBatchCommitment[:]...)
	dst = appendU64(dst, in.ExecutionHeight)
	dst = appendU64(dst, in.PreExchangeRevision)
	dst = appendU64(dst, in.PostExchangeRevision)
	dst = append(dst, in.ResultsHash[:]...)
	return sha256.Sum256(dst), nil
}
