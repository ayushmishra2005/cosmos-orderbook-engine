package matching

import (
	"errors"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

var (
	ErrNilSource   = errors.New("matching: nil source")
	ErrMakerMarket = errors.New("matching: maker market does not match incoming order")
	ErrMakerSide   = errors.New("matching: maker side is not opposite the incoming order")
	ErrInvariant   = errors.New("matching: quantity invariant failed")
)

// StopReason explains why matching stopped. Whether the incoming remainder
// may rest is MatchPlan.RestIncoming, not a separate stop reason.
type StopReason uint8

const (
	StopReasonFilled        StopReason = 1
	StopReasonBookExhausted StopReason = 2
	StopReasonPriceBoundary StopReason = 3
	StopReasonSelfTrade     StopReason = 4
	StopReasonVisitLimit    StopReason = 5
	StopReasonFOKRejected   StopReason = 6
	StopReasonExpired       StopReason = 7
)

// String returns a stable label for logs and tests.
func (r StopReason) String() string {
	switch r {
	case StopReasonFilled:
		return "filled"
	case StopReasonBookExhausted:
		return "book_exhausted"
	case StopReasonPriceBoundary:
		return "price_boundary"
	case StopReasonSelfTrade:
		return "self_trade"
	case StopReasonVisitLimit:
		return "visit_limit"
	case StopReasonFOKRejected:
		return "fok_rejected"
	case StopReasonExpired:
		return "expired"
	default:
		return "stop_reason_unset"
	}
}

// Fill is one execution. Price is the resting maker's tick, not the taker's.
type Fill struct {
	MakerOrderID domain.OrderID
	TakerOrderID domain.OrderID
	Price        domain.Price
	Quantity     domain.Quantity
}

// MatchInput is one matching attempt.
type MatchInput struct {
	Incoming domain.Order
	// ExecutionHeight is the consensus block height used for GTD expiry.
	// It is not a timestamp.
	ExecutionHeight uint64
	// MaxMakerVisits caps how many maker records this attempt may read,
	// including expired makers that are skipped. Zero means no cap.
	// Production callers must set a non-zero cap. A remainder that might
	// still cross the book is not rested when the cap stops the scan.
	MaxMakerVisits uint32
}

// MatchPlan is the deterministic result of one match.
// Fills are in price-time order. The matcher does not update maker residuals;
// a partial maker keeps its sequence, and the keeper subtracts Quantity.
// RestIncoming is set only when a limit GTC or GTD remainder can be written
// without crossing the remaining book.
type MatchPlan struct {
	Fills             []Fill
	RemainingQuantity domain.Quantity
	RestIncoming      bool
	StopReason        StopReason
}
