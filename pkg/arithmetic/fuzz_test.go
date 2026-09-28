package arithmetic

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func FuzzAdd(f *testing.F) {
	f.Add(uint64(0), uint64(0))
	f.Add(uint64(math.MaxUint64), uint64(1))
	f.Fuzz(func(t *testing.T, a, b uint64) {
		got, err := Add(a, b)
		var x, y, s big.Int
		x.SetUint64(a)
		y.SetUint64(b)
		s.Add(&x, &y)
		if !s.IsUint64() {
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("Add(%d, %d) = %d, %v", a, b, got, err)
			}
			return
		}
		if err != nil || got != s.Uint64() {
			t.Fatalf("Add(%d, %d) = %d, %v; want %s", a, b, got, err, s.String())
		}
	})
}

func FuzzSub(f *testing.F) {
	f.Add(uint64(5), uint64(5))
	f.Add(uint64(0), uint64(1))
	f.Fuzz(func(t *testing.T, a, b uint64) {
		got, err := Sub(a, b)
		if a < b {
			if !errors.Is(err, ErrUnderflow) {
				t.Fatalf("Sub(%d, %d) = %d, %v", a, b, got, err)
			}
			return
		}
		if err != nil || got != a-b {
			t.Fatalf("Sub(%d, %d) = %d, %v", a, b, got, err)
		}
	})
}

func FuzzMul(f *testing.F) {
	f.Add(uint64(math.MaxUint64), uint64(2))
	f.Add(uint64(0), uint64(math.MaxUint64))
	f.Fuzz(func(t *testing.T, a, b uint64) {
		got, err := Mul(a, b)
		var x, y, p big.Int
		x.SetUint64(a)
		y.SetUint64(b)
		p.Mul(&x, &y)
		if !p.IsUint64() {
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("Mul(%d, %d) = %d, %v", a, b, got, err)
			}
			return
		}
		if err != nil || got != p.Uint64() {
			t.Fatalf("Mul(%d, %d) = %d, %v; want %s", a, b, got, err, p.String())
		}
	})
}

func FuzzCeilDiv(f *testing.F) {
	f.Add(uint64(11), uint64(5))
	f.Add(uint64(math.MaxUint64), uint64(2))
	f.Add(uint64(1), uint64(0))
	f.Fuzz(func(t *testing.T, n, d uint64) {
		got, err := CeilDiv(n, d)
		if d == 0 {
			if !errors.Is(err, ErrDivisionByZero) {
				t.Fatalf("CeilDiv(%d, 0) = %d, %v", n, got, err)
			}
			return
		}
		var num, den, q, r big.Int
		num.SetUint64(n)
		den.SetUint64(d)
		q.QuoRem(&num, &den, &r)
		if r.Sign() != 0 {
			q.Add(&q, big.NewInt(1))
		}
		if err != nil || got != q.Uint64() {
			t.Fatalf("CeilDiv(%d, %d) = %d, %v; want %s", n, d, got, err, q.String())
		}
	})
}

func FuzzBaseAmount(f *testing.F) {
	f.Add(uint64(10), uint64(1))
	f.Add(uint64(1), uint64(0))
	f.Fuzz(func(t *testing.T, qty, lot uint64) {
		got, err := BaseAmount(domain.Quantity(qty), lot)
		if lot == 0 {
			if !errors.Is(err, ErrInvalidLotSize) {
				t.Fatalf("lot 0: %v", err)
			}
			return
		}
		var q, l, p big.Int
		q.SetUint64(qty)
		l.SetUint64(lot)
		p.Mul(&q, &l)
		if !p.IsUint64() {
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("BaseAmount(%d, %d) = %d, %v", qty, lot, got, err)
			}
			return
		}
		if err != nil || got != p.Uint64() {
			t.Fatalf("BaseAmount(%d, %d) = %d, %v; want %s", qty, lot, got, err, p.String())
		}
	})
}

func FuzzNotional(f *testing.F) {
	f.Add(uint64(2), uint64(3), uint64(4))
	f.Add(uint64(1), uint64(1), uint64(0))
	f.Fuzz(func(t *testing.T, qty, price, atoms uint64) {
		got, err := Notional(domain.Quantity(qty), domain.Price(price), atoms)
		if atoms == 0 {
			if !errors.Is(err, ErrInvalidTickValue) {
				t.Fatalf("atoms 0: %v", err)
			}
			return
		}
		var q, p, a, prod big.Int
		q.SetUint64(qty)
		p.SetUint64(price)
		a.SetUint64(atoms)
		prod.Mul(&q, &p)
		prod.Mul(&prod, &a)
		if !prod.IsUint64() {
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("Notional(%d, %d, %d) = %d, %v", qty, price, atoms, got, err)
			}
			return
		}
		if err != nil || got != prod.Uint64() {
			t.Fatalf("Notional(%d, %d, %d) = %d, %v; want %s", qty, price, atoms, got, err, prod.String())
		}
	})
}

func FuzzFee(f *testing.F) {
	f.Add(uint64(1000), uint64(25), uint64(10_000))
	f.Add(uint64(math.MaxUint64), uint64(math.MaxUint64), uint64(2))
	f.Add(uint64(1), uint64(1), uint64(0))
	f.Fuzz(func(t *testing.T, notional, numerator, denominator uint64) {
		if denominator == 0 {
			_, err := Fee(notional, numerator, denominator)
			if !errors.Is(err, ErrDivisionByZero) {
				t.Fatalf("denominator 0: %v", err)
			}
			return
		}
		assertFeeOracle(t, notional, numerator, denominator)
	})
}
