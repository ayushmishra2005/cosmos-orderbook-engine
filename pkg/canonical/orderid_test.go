package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func goldenInput() OrderIDInput {
	owner := make([]byte, 20)
	for i := range owner {
		owner[i] = byte(i + 1)
	}
	return OrderIDInput{
		ChainID:            "testing",
		ExchangeInstanceID: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Owner:              owner,
		MarketID:           42,
		CommandNonce:       7,
	}
}

func TestOrderIDGolden(t *testing.T) {
	t.Parallel()

	in := goldenInput()
	preimage, err := orderIDPreimage(in)
	if err != nil {
		t.Fatal(err)
	}
	id, err := HashOrderID(in)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(preimage)
	if id != sum {
		t.Fatal("HashOrderID diverged from the documented preimage")
	}

	// Frozen vector for the input in goldenInput. Update both lines together
	// if orderIDDomainSeparator or the preimage layout changes.
	const wantPreimage = "0000000000000023636f736d6f732d6f72646572626f6f6b2d656e67696e652f6f726465722d69642f7631000000000000000774657374696e670000000000000008000000000000000100000000000000140102030405060708090a0b0c0d0e0f1011121314000000000000002a0000000000000007"
	const wantID = "adc219aa3828c75c21e54f9f3b62222cd9ab831943b5fe845eb03a34ac5f626f"

	if hex.EncodeToString(preimage) != wantPreimage {
		t.Fatalf("preimage\n got %x\nwant %s", preimage, wantPreimage)
	}
	if id.String() != wantID {
		t.Fatalf("id %s", id)
	}
}

func TestOrderIDStableAndSeparated(t *testing.T) {
	t.Parallel()

	in := goldenInput()
	a, err := HashOrderID(in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashOrderID(in)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a.IsZero() {
		t.Fatalf("unstable id %s", a)
	}

	mutated := []OrderIDInput{
		{ChainID: "testing-2", ExchangeInstanceID: in.ExchangeInstanceID, Owner: in.Owner, MarketID: in.MarketID, CommandNonce: in.CommandNonce},
		{ChainID: in.ChainID, ExchangeInstanceID: []byte{2}, Owner: in.Owner, MarketID: in.MarketID, CommandNonce: in.CommandNonce},
		{ChainID: in.ChainID, ExchangeInstanceID: in.ExchangeInstanceID, Owner: []byte("other-owner"), MarketID: in.MarketID, CommandNonce: in.CommandNonce},
		{ChainID: in.ChainID, ExchangeInstanceID: in.ExchangeInstanceID, Owner: in.Owner, MarketID: in.MarketID + 1, CommandNonce: in.CommandNonce},
		{ChainID: in.ChainID, ExchangeInstanceID: in.ExchangeInstanceID, Owner: in.Owner, MarketID: in.MarketID, CommandNonce: in.CommandNonce + 1},
	}
	for i, other := range mutated {
		id, err := HashOrderID(other)
		if err != nil {
			t.Fatal(err)
		}
		if id == a {
			t.Fatalf("mutation %d kept the same id", i)
		}
	}

	// Length prefixes stop chain-id/instance concatenation from colliding.
	left, err := HashOrderID(OrderIDInput{
		ChainID: "ab", ExchangeInstanceID: []byte("c"), Owner: []byte{1}, MarketID: 1, CommandNonce: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := HashOrderID(OrderIDInput{
		ChainID: "a", ExchangeInstanceID: []byte("bc"), Owner: []byte{1}, MarketID: 1, CommandNonce: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("length prefix did not separate chain id from instance id")
	}
	if bytes.Equal(left[:], right[:]) {
		t.Fatal("equal bytes")
	}
}

func TestOrderIDRejectsInput(t *testing.T) {
	t.Parallel()

	base := goldenInput()
	cases := []OrderIDInput{
		{},
		{ChainID: "", ExchangeInstanceID: base.ExchangeInstanceID, Owner: base.Owner, MarketID: 1, CommandNonce: 1},
		{ChainID: "ok", ExchangeInstanceID: nil, Owner: base.Owner, MarketID: 1, CommandNonce: 1},
		{ChainID: "ok", ExchangeInstanceID: []byte{1}, Owner: nil, MarketID: 1, CommandNonce: 1},
		{ChainID: "ok", ExchangeInstanceID: []byte{1}, Owner: []byte{1}, MarketID: 0, CommandNonce: 1},
	}
	for i, in := range cases {
		if _, err := HashOrderID(in); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	long := base
	long.ChainID = string(bytes.Repeat([]byte("a"), maxChainIDLen+1))
	if _, err := HashOrderID(long); !errors.Is(err, ErrInvalidChainID) {
		t.Fatalf("long chain: %v", err)
	}
}
