package types

import (
	"bytes"
	"errors"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const (
	// FeeDenominator is the parts-per-million divisor.
	// Rates above this are rejected so a fee cannot exceed its gross.
	FeeDenominator uint64 = 1_000_000
	codecVersion   byte   = 1
)

// FeeCollectorOwner is the reserved balance owner that receives protocol fees.
// It is not an account that can place orders. Do not mutate this slice.
var FeeCollectorOwner = []byte("exchange/fee-collector")

var (
	ErrNotFound            = errors.New("exchange: not found")
	ErrExists              = errors.New("exchange: already exists")
	ErrDisabled            = errors.New("exchange: market disabled")
	ErrInsufficientBalance = errors.New("exchange: insufficient balance")
	ErrWrongOwner          = errors.New("exchange: wrong owner")
	ErrWrongNonce          = errors.New("exchange: wrong command nonce")
	ErrFOKRejected         = errors.New("exchange: fill or kill rejected")
	ErrExpired             = errors.New("exchange: order expired")
	ErrCorrupt             = errors.New("exchange: corrupt state")
	ErrFeeCollector        = errors.New("exchange: fee collector cannot trade")
	ErrInvalidFee          = errors.New("exchange: invalid fee")
	ErrInvalidVisits       = errors.New("exchange: invalid maker visit cap")
	ErrSameAsset           = errors.New("exchange: base and quote assets must differ")
	ErrNegativeHeight      = errors.New("exchange: negative block height")
	ErrCrossed             = errors.New("exchange: resting book would cross")
	ErrBound               = errors.New("exchange: expiration bound must be positive")
	ErrSettlement          = errors.New("exchange: settlement invariant failed")
)

// Market is one trading pair. Fees are parts per million of the filled gross.
type Market struct {
	ID                      domain.MarketID
	BaseAssetID             domain.AssetID
	QuoteAssetID            domain.AssetID
	BaseLotSize             uint64
	QuoteAtomsPerTickPerLot uint64
	MakerFeePPM             uint64
	TakerFeePPM             uint64
	MaxMakerVisits          uint32
	Enabled                 bool
}

// Validate checks the market fields the keeper will store.
func (m Market) Validate() error {
	if m.ID == 0 {
		return domain.ErrInvalidMarket
	}
	if m.BaseAssetID == 0 || m.QuoteAssetID == 0 {
		return domain.ErrInvalidAsset
	}
	if m.BaseAssetID == m.QuoteAssetID {
		return ErrSameAsset
	}
	if m.BaseLotSize == 0 {
		return domain.ErrInvalidQuantity
	}
	if m.QuoteAtomsPerTickPerLot == 0 {
		return domain.ErrInvalidPrice
	}
	if m.MaxMakerVisits == 0 {
		return ErrInvalidVisits
	}
	if m.MakerFeePPM > FeeDenominator || m.TakerFeePPM > FeeDenominator {
		return ErrInvalidFee
	}
	return nil
}

// Balance is an integer atom balance. Neither field may be negative;
// the type itself cannot represent a negative amount.
type Balance struct {
	Available uint64
	Locked    uint64
}

// StoredOrder is the active-order record. TakerGross and MakerGross are the
// filled gross already used for that role's cumulative fee. A buy accumulates
// base atoms. A sell accumulates quote atoms. The two roles are not mixed.
type StoredOrder struct {
	Order      domain.Order
	TakerGross uint64
	MakerGross uint64
}

// Trade is one fill. Sequence is the per-market trade sequence.
// MakerFee and TakerFee are the incremental fees charged on this fill.
type Trade struct {
	MarketID     domain.MarketID
	Sequence     uint64
	MakerOrderID domain.OrderID
	TakerOrderID domain.OrderID
	Price        domain.Price
	Quantity     domain.Quantity
	BaseAmount   uint64
	QuoteAmount  uint64
	MakerFee     uint64
	TakerFee     uint64
	Buyer        []byte
	Seller       []byte
}

// PlaceOrderCommand is one place request. The order ID and sequence are
// assigned by the keeper. Quantity is both the original and the initial remaining.
type PlaceOrderCommand struct {
	Owner        []byte
	MarketID     domain.MarketID
	Side         domain.Side
	Type         domain.OrderType
	TimeInForce  domain.TimeInForce
	Price        domain.Price
	Quantity     domain.Quantity
	ExpiryHeight uint64
	CommandNonce uint64
}

// CancelOrderCommand cancels by order ID. The book is not scanned.
type CancelOrderCommand struct {
	Owner        []byte
	OrderID      domain.OrderID
	CommandNonce uint64
}

// PlaceResult is the deterministic outcome of an accepted place command.
type PlaceResult struct {
	OrderID   domain.OrderID
	Fills     []Trade
	Remaining domain.Quantity
	Rested    bool
	Stop      uint8
}

// CancelResult reports the atoms moved from locked back to available.
type CancelResult struct {
	OrderID  domain.OrderID
	AssetID  domain.AssetID
	Released uint64
}

// IsFeeCollector reports whether owner is the reserved fee account.
func IsFeeCollector(owner []byte) bool {
	return bytes.Equal(owner, FeeCollectorOwner)
}
