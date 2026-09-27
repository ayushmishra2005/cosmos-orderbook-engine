package domain

// Order is a pure domain order. Transport and protobuf encodings are separate
// and must not be used as the consensus representation.
type Order struct {
	ID    OrderID
	Owner []byte // Canonical address bytes, not bech32 text.

	MarketID    MarketID
	Side        Side
	Type        OrderType
	TimeInForce TimeInForce

	// Price is a tick count. Market orders carry the worst acceptable tick.
	Price Price
	// OriginalQuantity and RemainingQuantity are lot counts.
	OriginalQuantity  Quantity
	RemainingQuantity Quantity

	// Sequence is the per-market acceptance order. Zero means the order is
	// not yet a resting-book position.
	Sequence Sequence
	// ExpiryHeight is the first block height at which a GTD order expires.
	// It is zero unless TimeInForce is GTD.
	ExpiryHeight uint64
	// CommandNonce is the account nonce of the command that created the order.
	// It is an input to the order ID. The matcher does not advance nonces.
	CommandNonce uint64
}

// ValidateOwner checks the canonical address length used in IDs and keys.
func ValidateOwner(owner []byte) error {
	if len(owner) == 0 || len(owner) > MaxOwnerLength {
		return ErrInvalidOwner
	}
	return nil
}

// ValidateIncoming checks an order before it is matched.
// Sequence may be zero: the keeper assigns it only if the order rests.
func (o Order) ValidateIncoming() error {
	if err := validateCommon(o); err != nil {
		return err
	}
	// Market orders are IOC or FOK. They must carry a worst-price bound and
	// must never rest, so GTC and GTD are rejected here.
	if o.Type == OrderTypeMarket && o.TimeInForce != TimeInForceIOC && o.TimeInForce != TimeInForceFOK {
		return ErrInvalidTimeInForce
	}
	return nil
}

// ValidateResting checks an order that already occupies a book position.
func (o Order) ValidateResting() error {
	if err := validateCommon(o); err != nil {
		return err
	}
	if o.Type != OrderTypeLimit {
		return ErrInvalidOrderType
	}
	if o.TimeInForce != TimeInForceGTC && o.TimeInForce != TimeInForceGTD {
		return ErrInvalidTimeInForce
	}
	if o.Sequence == 0 {
		return ErrInvalidSequence
	}
	return nil
}

func validateCommon(o Order) error {
	if o.ID.IsZero() {
		return ErrInvalidOrderID
	}
	if err := ValidateOwner(o.Owner); err != nil {
		return err
	}
	if o.MarketID == 0 {
		return ErrInvalidMarket
	}
	if !o.Side.Valid() {
		return ErrInvalidSide
	}
	if !o.Type.Valid() {
		return ErrInvalidOrderType
	}
	if !o.TimeInForce.Valid() {
		return ErrInvalidTimeInForce
	}
	// Tick 0 is not a valid price. Market orders still need a worst tick.
	if o.Price == 0 {
		return ErrInvalidPrice
	}
	if o.OriginalQuantity == 0 || o.RemainingQuantity == 0 || o.RemainingQuantity > o.OriginalQuantity {
		return ErrInvalidQuantity
	}
	if o.TimeInForce == TimeInForceGTD {
		if o.ExpiryHeight == 0 {
			return ErrInvalidExpiry
		}
	} else if o.ExpiryHeight != 0 {
		return ErrInvalidExpiry
	}
	return nil
}

// ExpiredAt reports whether a GTD order can no longer trade at height.
// ExpiryHeight is the first expired height, so the order trades while
// height < ExpiryHeight. GTC orders do not expire.
func (o Order) ExpiredAt(height uint64) bool {
	return o.TimeInForce == TimeInForceGTD && height >= o.ExpiryHeight
}
