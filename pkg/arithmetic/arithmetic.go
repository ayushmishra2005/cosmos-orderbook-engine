package arithmetic

import (
	"errors"
	"math/bits"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

var (
	ErrOverflow         = errors.New("arithmetic: overflow")
	ErrUnderflow        = errors.New("arithmetic: underflow")
	ErrDivisionByZero   = errors.New("arithmetic: division by zero")
	ErrInvalidLotSize   = errors.New("arithmetic: invalid base lot size")
	ErrInvalidTickValue = errors.New("arithmetic: invalid quote atoms per tick per lot")
)

// Add returns a + b, or ErrOverflow if the sum does not fit in uint64.
func Add(a, b uint64) (uint64, error) {
	sum, carry := bits.Add64(a, b, 0)
	if carry != 0 {
		return 0, ErrOverflow
	}
	return sum, nil
}

// Sub returns a - b, or ErrUnderflow if b > a.
func Sub(a, b uint64) (uint64, error) {
	diff, borrow := bits.Sub64(a, b, 0)
	if borrow != 0 {
		return 0, ErrUnderflow
	}
	return diff, nil
}

// Mul returns a * b, or ErrOverflow if the product does not fit in uint64.
func Mul(a, b uint64) (uint64, error) {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return 0, ErrOverflow
	}
	return lo, nil
}

// CeilDiv returns ceil(numerator / denominator).
// A zero numerator is zero. Denominator zero is an error.
// For d >= 1, ceil(n/d) always fits in uint64 when n does.
func CeilDiv(numerator, denominator uint64) (uint64, error) {
	if denominator == 0 {
		return 0, ErrDivisionByZero
	}
	if numerator == 0 {
		return 0, nil
	}
	q, r := bits.Div64(0, numerator, denominator)
	if r == 0 {
		return q, nil
	}
	return Add(q, 1)
}

// BaseAmount returns quantityLots * baseLotSize in base atoms.
func BaseAmount(quantity domain.Quantity, baseLotSize uint64) (uint64, error) {
	if baseLotSize == 0 {
		return 0, ErrInvalidLotSize
	}
	return Mul(uint64(quantity), baseLotSize)
}

// Notional returns quantityLots * priceTicks * quoteAtomsPerTickPerLot
// in quote atoms. A zero quote-atoms parameter is a broken market and errors
// even when the quantity is zero, so a misconfigured market cannot settle at
// a zero quote amount.
func Notional(quantity domain.Quantity, price domain.Price, quoteAtomsPerTickPerLot uint64) (uint64, error) {
	if quoteAtomsPerTickPerLot == 0 {
		return 0, ErrInvalidTickValue
	}
	product, err := Mul(uint64(quantity), uint64(price))
	if err != nil {
		return 0, err
	}
	return Mul(product, quoteAtomsPerTickPerLot)
}

// Fee returns ceil(notional * numerator / denominator).
// The product is evaluated in 128 bits. Rounding is away from zero so the
// charged fee is never below the exact rate. The result errors if the rounded
// fee does not fit in uint64 or if denominator is zero.
func Fee(notional, numerator, denominator uint64) (uint64, error) {
	if denominator == 0 {
		return 0, ErrDivisionByZero
	}
	if notional == 0 || numerator == 0 {
		return 0, nil
	}
	hi, lo := bits.Mul64(notional, numerator)
	// bits.Div64 panics when the quotient does not fit in uint64.
	if hi >= denominator {
		return 0, ErrOverflow
	}
	q, r := bits.Div64(hi, lo, denominator)
	if r == 0 {
		return q, nil
	}
	return Add(q, 1)
}
