package domain

import "errors"

var (
	ErrInvalidOrderID     = errors.New("domain: invalid order id")
	ErrInvalidOwner       = errors.New("domain: invalid owner")
	ErrInvalidMarket      = errors.New("domain: invalid market")
	ErrInvalidAsset       = errors.New("domain: invalid asset")
	ErrInvalidSide        = errors.New("domain: invalid side")
	ErrInvalidOrderType   = errors.New("domain: invalid order type")
	ErrInvalidTimeInForce = errors.New("domain: invalid time in force")
	ErrInvalidPrice       = errors.New("domain: invalid price")
	ErrInvalidQuantity    = errors.New("domain: invalid quantity")
	ErrInvalidExpiry      = errors.New("domain: invalid expiry")
	ErrInvalidSequence    = errors.New("domain: invalid sequence")
	ErrSequenceOverflow   = errors.New("domain: sequence overflow")
	ErrNonceOverflow      = errors.New("domain: command nonce overflow")
)
