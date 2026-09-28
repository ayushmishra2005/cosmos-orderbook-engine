package orderbook

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestSignedCommandBytesMatchCanonical(t *testing.T) {
	priv := testPriv()
	signer := privSigner{priv: priv}
	client := &Client{chainID: "orderbook-test", instance: []byte("orderbook-v1")}

	place, err := client.SignPlaceOrder(signer, 7, PlaceCommand{
		MarketID: 1, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, QuantityLots: 15, PriceTicks: 42,
		ClientOrderID: []byte("cid-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPlace := canonical.Command{
		ProtocolVersion: canonical.BatchCommandVersion, ChainID: "orderbook-test",
		ExchangeInstanceID: []byte("orderbook-v1"), Owner: signer.Address(),
		Nonce: 7, Type: canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID: 1, Side: domain.SideSell, Type: domain.OrderTypeLimit,
			TimeInForce: domain.TimeInForceGTC, Quantity: 15, Price: 42,
			ClientOrderID: []byte("cid-1"),
		},
	}
	assertSameSignBytes(t, place, wantPlace)
	got, err := canonical.CommandSignBytes(place)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != goldenPlaceSignBytes {
		t.Fatalf("place sign bytes %x", got)
	}

	var id domain.OrderID
	id[0] = 0xab
	id[31] = 0xcd
	cancel, err := client.SignCancelOrder(signer, 8, id)
	if err != nil {
		t.Fatal(err)
	}
	wantCancel := canonical.Command{
		ProtocolVersion: canonical.BatchCommandVersion, ChainID: "orderbook-test",
		ExchangeInstanceID: []byte("orderbook-v1"), Owner: signer.Address(),
		Nonce: 8, Type: canonical.CommandTypeCancel, Cancel: &canonical.Cancel{OrderID: id},
	}
	assertSameSignBytes(t, cancel, wantCancel)
	gotCancel, err := canonical.CommandSignBytes(cancel)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(gotCancel) != goldenCancelSignBytes {
		t.Fatalf("cancel sign bytes %x", gotCancel)
	}
}

// Fixed secrets. These are the canonical batch-command bytes x/batch verifies.
const (
	goldenPlaceSignBytes  = "0000000000000021636f736d6f732d6f72646572626f6f6b2f62617463682d636f6d6d616e642f763100000001000000000000000e6f72646572626f6f6b2d74657374000000000000000c6f72646572626f6f6b2d763100000000000000142f460bee28ebf648e361a8301a699314e0dc45b10000000000000007010000000000000001020101000000000000000f000000000000002a000000000000000000000000000000056369642d31"
	goldenCancelSignBytes = "0000000000000021636f736d6f732d6f72646572626f6f6b2f62617463682d636f6d6d616e642f763100000001000000000000000e6f72646572626f6f6b2d74657374000000000000000c6f72646572626f6f6b2d763100000000000000142f460bee28ebf648e361a8301a699314e0dc45b1000000000000000802ab000000000000000000000000000000000000000000000000000000000000cd"
)

func assertSameSignBytes(t *testing.T, signed, want canonical.Command) {
	t.Helper()
	got, err := canonical.CommandSignBytes(signed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := canonical.CommandSignBytes(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("sign bytes differ\n got %x\nwant %x", got, plain)
	}
	decoded, err := canonical.DecodeCommandSignBytes(got)
	if err != nil {
		t.Fatal(err)
	}
	again, err := canonical.CommandSignBytes(decoded)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("decode round trip: %v", err)
	}
	pub := &secp256k1.PubKey{Key: append([]byte(nil), signed.PubKey...)}
	if !pub.VerifySignature(got, signed.Signature) {
		t.Fatal("signature rejected")
	}
	if !bytes.Equal(sdk.AccAddress(pub.Address()), signed.Owner) {
		t.Fatal("public key does not match owner")
	}
	if len(signed.PubKey) != secp256k1.PubKeySize || (signed.PubKey[0] != 0x02 && signed.PubKey[0] != 0x03) {
		t.Fatal("public key encoding")
	}
	if len(signed.Signature) != 64 {
		t.Fatal("signature length")
	}
}

func testPriv() *secp256k1.PrivKey {
	secret := bytes.Repeat([]byte{0x42}, 32)
	return secp256k1.GenPrivKeyFromSecret(secret)
}

type privSigner struct {
	priv *secp256k1.PrivKey
}

func (s privSigner) Address() sdk.AccAddress {
	return sdk.AccAddress(s.priv.PubKey().Address())
}

func (s privSigner) PubKey() cryptotypes.PubKey { return s.priv.PubKey() }

func (s privSigner) Sign(bz []byte) ([]byte, error) { return s.priv.Sign(bz) }
