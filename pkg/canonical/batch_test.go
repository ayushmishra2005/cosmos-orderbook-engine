package canonical

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestCommandSignBytesLayout(t *testing.T) {
	var id domain.OrderID
	for i := range id {
		id[i] = byte(i + 1)
	}
	cmd := Command{
		ProtocolVersion:    BatchCommandVersion,
		ChainID:            "c",
		ExchangeInstanceID: []byte{0x09},
		Owner:              []byte{0x01, 0x02},
		Nonce:              7,
		Type:               CommandTypeCancel,
		Cancel:             &Cancel{OrderID: id},
		PubKey:             []byte{0x02, 0xff},
		Signature:          []byte{0xab},
	}
	got, err := CommandSignBytes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	want := appendLenPrefixed(nil, []byte(BatchCommandDomain))
	want = appendU32(want, BatchCommandVersion)
	want = appendLenPrefixed(want, []byte("c"))
	want = appendLenPrefixed(want, []byte{0x09})
	want = appendLenPrefixed(want, []byte{0x01, 0x02})
	want = appendU64(want, 7)
	want = append(want, byte(CommandTypeCancel))
	want = append(want, id[:]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("sign bytes\n got %x\nwant %x", got, want)
	}
	withoutAuth := cmd
	withoutAuth.PubKey = nil
	withoutAuth.Signature = nil
	again, err := CommandSignBytes(withoutAuth)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("auth bytes changed the signing payload: %v", err)
	}
	if _, err := CommandSignBytes(Command{ProtocolVersion: 2, ChainID: "c", ExchangeInstanceID: []byte{1}, Owner: []byte{1}, Type: CommandTypeCancel, Cancel: &Cancel{OrderID: id}}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatal(err)
	}
}

func TestBatchIDOrder(t *testing.T) {
	a := samplePlace(1, 10)
	b := samplePlace(2, 11)
	one, err := HashBatchID(1, 4, []Command{a, b})
	if err != nil {
		t.Fatal(err)
	}
	two, err := HashBatchID(1, 4, []Command{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatal("identical batch input changed the batch id")
	}
	swapped, err := HashBatchID(1, 4, []Command{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if one == swapped {
		t.Fatal("command order did not change the batch id")
	}
	preimage := appendLenPrefixed(nil, []byte(BatchIDDomain))
	preimage = appendU64(preimage, 1)
	preimage = appendU64(preimage, 4)
	preimage = appendU64(preimage, 2)
	for _, cmd := range []Command{a, b} {
		sign, err := CommandSignBytes(cmd)
		if err != nil {
			t.Fatal(err)
		}
		preimage = appendLenPrefixed(preimage, sign)
		preimage = appendLenPrefixed(preimage, cmd.PubKey)
		preimage = appendLenPrefixed(preimage, cmd.Signature)
	}
	if sha256.Sum256(preimage) != one {
		t.Fatal("batch id layout drifted")
	}
}

func samplePlace(nonce, price uint64) Command {
	return Command{
		ProtocolVersion:    BatchCommandVersion,
		ChainID:            "chain",
		ExchangeInstanceID: []byte("orderbook-v1"),
		Owner:              bytes.Repeat([]byte{byte(nonce)}, 20),
		Nonce:              nonce,
		Type:               CommandTypePlace,
		Place: &Place{
			MarketID:    1,
			Side:        domain.SideBuy,
			Type:        domain.OrderTypeLimit,
			TimeInForce: domain.TimeInForceGTC,
			Quantity:    1,
			Price:       domain.Price(price),
		},
		PubKey:    bytes.Repeat([]byte{0x02}, 33),
		Signature: bytes.Repeat([]byte{0x11}, 64),
	}
}
