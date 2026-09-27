package domain

import "encoding/hex"

// MarketID, AssetID, Price, Quantity, and Sequence are protocol integers.
// Price is a tick count. Quantity is a lot count. Neither is a decimal.
type (
	MarketID uint64
	AssetID  uint64
	Price    uint64
	Quantity uint64
	Sequence uint64
)

// MaxOwnerLength is the maximum canonical address length accepted by the
// protocol. It matches the Cosmos SDK address length cap so a later keeper
// can store addresses without a second encoding.
const MaxOwnerLength = 255

// OrderID is a 32-byte SHA-256 identifier. It is not a random UUID.
type OrderID [32]byte

// IsZero reports whether the identifier is the all-zero value.
func (id OrderID) IsZero() bool {
	return id == OrderID{}
}

// String returns the hex encoding of the identifier.
func (id OrderID) String() string {
	return hex.EncodeToString(id[:])
}

// Side is the book side of an order. Numeric values are consensus-stable.
type Side uint8

const (
	SideBuy  Side = 1
	SideSell Side = 2
)

// Valid reports whether s is a known side.
func (s Side) Valid() bool {
	return s == SideBuy || s == SideSell
}

// OrderType distinguishes resting limits from orders that must not rest.
// Numeric values are consensus-stable.
type OrderType uint8

const (
	OrderTypeLimit  OrderType = 1
	OrderTypeMarket OrderType = 2
)

// Valid reports whether t is a known order type.
func (t OrderType) Valid() bool {
	return t == OrderTypeLimit || t == OrderTypeMarket
}

// TimeInForce controls whether a remainder may rest.
// Numeric values are consensus-stable.
type TimeInForce uint8

const (
	TimeInForceGTC TimeInForce = 1
	TimeInForceIOC TimeInForce = 2
	TimeInForceFOK TimeInForce = 3
	TimeInForceGTD TimeInForce = 4
)

// Valid reports whether t is a known time in force.
func (t TimeInForce) Valid() bool {
	return t == TimeInForceGTC || t == TimeInForceIOC || t == TimeInForceFOK || t == TimeInForceGTD
}
