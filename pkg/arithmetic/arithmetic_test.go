package arithmetic

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestAddSubMul(t *testing.T) {
	t.Parallel()

	sum, err := Add(1, 2)
	if err != nil || sum != 3 {
		t.Fatalf("add = %d, %v", sum, err)
	}
	if _, err := Add(math.MaxUint64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("add overflow: %v", err)
	}
	if got, err := Add(math.MaxUint64, 0); err != nil || got != math.MaxUint64 {
		t.Fatalf("add max+0 = %d, %v", got, err)
	}

	diff, err := Sub(5, 5)
	if err != nil || diff != 0 {
		t.Fatalf("sub = %d, %v", diff, err)
	}
	if _, err := Sub(0, 1); !errors.Is(err, ErrUnderflow) {
		t.Fatalf("sub underflow: %v", err)
	}

	prod, err := Mul(math.MaxUint64, 1)
	if err != nil || prod != math.MaxUint64 {
		t.Fatalf("mul = %d, %v", prod, err)
	}
	if got, err := Mul(0, math.MaxUint64); err != nil || got != 0 {
		t.Fatalf("mul zero = %d, %v", got, err)
	}
	if _, err := Mul(math.MaxUint64, 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("mul overflow: %v", err)
	}
}

func TestCeilDiv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n, d uint64
		want uint64
		err  error
	}{
		{n: 0, d: 5, want: 0},
		{n: 10, d: 5, want: 2},
		{n: 11, d: 5, want: 3},
		{n: math.MaxUint64, d: 1, want: math.MaxUint64},
		{n: math.MaxUint64, d: math.MaxUint64, want: 1},
		{n: math.MaxUint64, d: 2, want: 1 << 63},
		{n: 5, d: 0, err: ErrDivisionByZero},
	}
	for _, tt := range tests {
		got, err := CeilDiv(tt.n, tt.d)
		if !errors.Is(err, tt.err) || (err == nil && got != tt.want) {
			t.Fatalf("CeilDiv(%d, %d) = %d, %v; want %d, %v", tt.n, tt.d, got, err, tt.want, tt.err)
		}
	}
}

func TestBaseAmountAndNotional(t *testing.T) {
	t.Parallel()

	base, err := BaseAmount(5, 1_000_000)
	if err != nil || base != 5_000_000 {
		t.Fatalf("base = %d, %v", base, err)
	}
	if _, err := BaseAmount(1, 0); !errors.Is(err, ErrInvalidLotSize) {
		t.Fatalf("lot size: %v", err)
	}
	if _, err := BaseAmount(math.MaxUint64, 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("base overflow: %v", err)
	}

	// 5 lots * 980 ticks * 100 quote atoms per tick per lot.
	quote, err := Notional(5, 980, 100)
	if err != nil || quote != 490_000 {
		t.Fatalf("notional = %d, %v", quote, err)
	}
	if _, err := Notional(1, 1, 0); !errors.Is(err, ErrInvalidTickValue) {
		t.Fatalf("tick value: %v", err)
	}
	if _, err := Notional(math.MaxUint64, 2, 2); !errors.Is(err, ErrOverflow) {
		t.Fatalf("notional overflow: %v", err)
	}
	zero, err := Notional(0, domain.Price(10), 1)
	if err != nil || zero != 0 {
		t.Fatalf("zero qty notional = %d, %v", zero, err)
	}
}

func TestFee(t *testing.T) {
	t.Parallel()

	// 25 bps of 1000 = 2.5, rounded away from zero to 3.
	fee, err := Fee(1000, 25, 10_000)
	if err != nil || fee != 3 {
		t.Fatalf("fee = %d, %v", fee, err)
	}
	fee, err = Fee(10, 1, 3)
	if err != nil || fee != 4 {
		t.Fatalf("ceil fee = %d, %v", fee, err)
	}
	fee, err = Fee(10, 1, 2)
	if err != nil || fee != 5 {
		t.Fatalf("exact fee = %d, %v", fee, err)
	}
	fee, err = Fee(1, 1, 2)
	if err != nil || fee != 1 {
		t.Fatalf("small fee = %d, %v", fee, err)
	}
	if got, err := Fee(0, 5, 10); err != nil || got != 0 {
		t.Fatalf("zero notional fee = %d, %v", got, err)
	}
	if got, err := Fee(5, 0, 10); err != nil || got != 0 {
		t.Fatalf("zero numerator fee = %d, %v", got, err)
	}
	if _, err := Fee(1, 1, 0); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("fee den: %v", err)
	}
	if _, err := Fee(math.MaxUint64, 2, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("fee overflow: %v", err)
	}
	if _, err := Fee(math.MaxUint64, math.MaxUint64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("fee overflow max: %v", err)
	}
}

func TestFeeMatchesBigInt(t *testing.T) {
	t.Parallel()

	cases := [][3]uint64{
		{1, 1, 1},
		{1000, 25, 10_000},
		{math.MaxUint64, 1, math.MaxUint64},
		{math.MaxUint64, math.MaxUint64, math.MaxUint64},
		{(1 << 63), (1 << 63), 3},
	}
	for _, c := range cases {
		assertFeeOracle(t, c[0], c[1], c[2])
	}
}

func assertFeeOracle(t *testing.T, notional, numerator, denominator uint64) {
	t.Helper()
	got, err := Fee(notional, numerator, denominator)

	var n, u, d, q, r big.Int
	n.SetUint64(notional)
	u.SetUint64(numerator)
	d.SetUint64(denominator)
	n.Mul(&n, &u)
	q.QuoRem(&n, &d, &r)
	if r.Sign() != 0 {
		q.Add(&q, big.NewInt(1))
	}
	if !q.IsUint64() {
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("Fee(%d, %d, %d) = %d, %v; want overflow", notional, numerator, denominator, got, err)
		}
		return
	}
	if err != nil || got != q.Uint64() {
		t.Fatalf("Fee(%d, %d, %d) = %d, %v; want %s", notional, numerator, denominator, got, err, q.String())
	}
}
