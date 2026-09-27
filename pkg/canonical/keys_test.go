package canonical

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestComplementPriceInvolution(t *testing.T) {
	t.Parallel()
	for _, p := range []domain.Price{1, 2, 980, ^domain.Price(0)} {
		got := domain.Price(complementPrice(domain.Price(complementPrice(p))))
		if got != p {
			t.Fatalf("complement(%d) involution = %d", p, got)
		}
	}
}

func TestAskAndBidRoundTripAndLayout(t *testing.T) {
	t.Parallel()

	ask, err := EncodeAskKey(7, 1000, 3)
	if err != nil {
		t.Fatal(err)
	}
	bid, err := EncodeBidKey(7, 1000, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ask[0] != PrefixAskBook || bid[0] != PrefixBidBook {
		t.Fatalf("prefixes ask %x bid %x", ask[0], bid[0])
	}
	if bytes.Equal(ask, bid) {
		t.Fatal("ask and bid keys collided")
	}
	if _, _, _, err := DecodeAskKey(bid); err == nil {
		t.Fatal("bid decoded as ask")
	}
	if _, _, _, err := DecodeBidKey(ask); err == nil {
		t.Fatal("ask decoded as bid")
	}

	if got := binary.BigEndian.Uint64(ask[9:17]); got != 1000 {
		t.Fatalf("ask stores raw price %d", got)
	}
	if got := binary.BigEndian.Uint64(bid[9:17]); got != ^uint64(1000) {
		t.Fatalf("bid stores complement %x", got)
	}

	marketID, price, sequence, err := DecodeAskKey(ask)
	if err != nil || marketID != 7 || price != 1000 || sequence != 3 {
		t.Fatalf("decode ask %d %d %d %v", marketID, price, sequence, err)
	}
	marketID, price, sequence, err = DecodeBidKey(bid)
	if err != nil || marketID != 7 || price != 1000 || sequence != 3 {
		t.Fatalf("decode bid %d %d %d %v", marketID, price, sequence, err)
	}

	prefix, err := AskMarketPrefix(7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(ask, prefix) {
		t.Fatal("ask key missing market prefix")
	}
	bidPrefix, err := BidMarketPrefix(7)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(ask, bidPrefix) {
		t.Fatal("ask key matched bid prefix")
	}
}

func TestAskKeyByteOrder(t *testing.T) {
	t.Parallel()

	// Expected ascending byte order: market, then price, then sequence.
	ordered := []struct {
		market   domain.MarketID
		price    domain.Price
		sequence domain.Sequence
	}{
		{1, 1, 9},
		{1, 100, 1},
		{1, 100, 2},
		{1, 101, 1},
		{1, ^domain.Price(0), 1},
		{2, 1, 1},
	}
	keys := make([][]byte, len(ordered))
	for i, item := range ordered {
		key, err := EncodeAskKey(item.market, item.price, item.sequence)
		if err != nil {
			t.Fatal(err)
		}
		marketID, price, sequence, err := DecodeAskKey(key)
		if err != nil || marketID != item.market || price != item.price || sequence != item.sequence {
			t.Fatalf("round trip %+v: %d %d %d %v", item, marketID, price, sequence, err)
		}
		keys[i] = key
	}
	assertStrictByteOrder(t, keys)
}

func TestBidKeyByteOrder(t *testing.T) {
	t.Parallel()

	// Expected ascending byte order: market, then highest price, then sequence.
	ordered := []struct {
		market   domain.MarketID
		price    domain.Price
		sequence domain.Sequence
	}{
		{1, ^domain.Price(0), 1},
		{1, 101, 1},
		{1, 100, 1},
		{1, 100, 2},
		{1, 1, 1},
		{2, ^domain.Price(0), 1},
	}
	keys := make([][]byte, len(ordered))
	for i, item := range ordered {
		key, err := EncodeBidKey(item.market, item.price, item.sequence)
		if err != nil {
			t.Fatal(err)
		}
		marketID, price, sequence, err := DecodeBidKey(key)
		if err != nil || marketID != item.market || price != item.price || sequence != item.sequence {
			t.Fatalf("round trip %+v: %d %d %d %v", item, marketID, price, sequence, err)
		}
		keys[i] = key
	}
	assertStrictByteOrder(t, keys)

	high, err := EncodeBidKey(1, math.MaxUint64, 1)
	if err != nil {
		t.Fatal(err)
	}
	low, err := EncodeBidKey(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(high, low) >= 0 {
		t.Fatal("max bid did not sort before min bid")
	}
	if got := binary.BigEndian.Uint64(high[9:17]); got != 0 {
		t.Fatalf("max price complement = %d", got)
	}
}

func TestBookKeyRejects(t *testing.T) {
	t.Parallel()

	if _, err := EncodeAskKey(0, 1, 1); !errors.Is(err, domain.ErrInvalidMarket) {
		t.Fatal(err)
	}
	if _, err := EncodeAskKey(1, 0, 1); !errors.Is(err, domain.ErrInvalidPrice) {
		t.Fatal(err)
	}
	if _, err := EncodeBidKey(1, 1, 0); !errors.Is(err, domain.ErrInvalidSequence) {
		t.Fatal(err)
	}

	var crafted [bookKeyLen]byte
	crafted[0] = PrefixAskBook
	binary.BigEndian.PutUint64(crafted[1:9], 1)
	binary.BigEndian.PutUint64(crafted[17:25], 1)
	if _, _, _, err := DecodeAskKey(crafted[:]); !errors.Is(err, domain.ErrInvalidPrice) {
		t.Fatalf("price 0: %v", err)
	}
	if _, _, _, err := DecodeAskKey(crafted[:8]); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("truncated: %v", err)
	}
	crafted[0] = PrefixBidBook
	if _, _, _, err := DecodeAskKey(crafted[:]); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("wrong prefix: %v", err)
	}
	withTail := append(append([]byte{}, crafted[:]...), 0xFF)
	if _, _, _, err := DecodeBidKey(withTail); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("tail: %v", err)
	}
}

func TestStateKeyFamilies(t *testing.T) {
	t.Parallel()

	id := domain.OrderID{1: 2, 31: 9}
	owner := bytes.Repeat([]byte{0xAB}, domain.MaxOwnerLength)

	active, err := EncodeActiveOrderKey(id)
	if err != nil {
		t.Fatal(err)
	}
	gotID, err := DecodeActiveOrderKey(active)
	if err != nil || gotID != id {
		t.Fatalf("active %s %v", gotID, err)
	}
	if _, err := EncodeActiveOrderKey(domain.OrderID{}); !errors.Is(err, domain.ErrInvalidOrderID) {
		t.Fatal(err)
	}

	open, err := EncodeOwnerOpenOrderKey(owner, 4, id)
	if err != nil {
		t.Fatal(err)
	}
	gotOwner, marketID, gotOpenID, err := DecodeOwnerOpenOrderKey(open)
	if err != nil || marketID != 4 || gotOpenID != id || !bytes.Equal(gotOwner, owner) {
		t.Fatalf("owner open %d %s %v", marketID, gotOpenID, err)
	}
	prefix, err := OwnerOpenOrderPrefix(owner)
	if err != nil || !bytes.HasPrefix(open, prefix) {
		t.Fatalf("owner prefix %v", err)
	}
	gotOwner[0] ^= 0xFF
	gotOwner2, _, _, err := DecodeOwnerOpenOrderKey(open)
	if err != nil || bytes.Equal(gotOwner, gotOwner2) {
		t.Fatal("decoded owner aliases the key")
	}

	client, err := EncodeActiveClientOrderKey([]byte("alice"), bytes.Repeat([]byte("x"), MaxClientOrderIDLength))
	if err != nil {
		t.Fatal(err)
	}
	gotOwner, clientID, err := DecodeActiveClientOrderKey(client)
	if err != nil || string(gotOwner) != "alice" || len(clientID) != MaxClientOrderIDLength {
		t.Fatalf("client id %q %v", clientID, err)
	}
	if _, err := EncodeActiveClientOrderKey([]byte("alice"), nil); !errors.Is(err, ErrInvalidClientOrderID) {
		t.Fatal(err)
	}
	if _, err := EncodeActiveClientOrderKey([]byte("alice"), bytes.Repeat([]byte("x"), MaxClientOrderIDLength+1)); !errors.Is(err, ErrInvalidClientOrderID) {
		t.Fatal(err)
	}
	clientPrefix, err := ActiveClientOrderPrefix([]byte("alice"))
	if err != nil || !bytes.HasPrefix(client, clientPrefix) {
		t.Fatal("client prefix")
	}

	exp1, err := EncodeExpirationKey(1, id)
	if err != nil {
		t.Fatal(err)
	}
	laterID := id
	laterID[31]++
	exp2, err := EncodeExpirationKey(1, laterID)
	if err != nil {
		t.Fatal(err)
	}
	exp3, err := EncodeExpirationKey(2, id)
	if err != nil {
		t.Fatal(err)
	}
	assertStrictByteOrder(t, [][]byte{exp1, exp2, exp3})
	height, gotExpID, err := DecodeExpirationKey(exp1)
	if err != nil || height != 1 || gotExpID != id {
		t.Fatalf("expiry %d %s %v", height, gotExpID, err)
	}
	if _, err := EncodeExpirationKey(0, id); !errors.Is(err, domain.ErrInvalidExpiry) {
		t.Fatal(err)
	}

	balance, err := EncodeBalanceKey([]byte("alice"), 8)
	if err != nil {
		t.Fatal(err)
	}
	gotOwner, assetID, err := DecodeBalanceKey(balance)
	if err != nil || string(gotOwner) != "alice" || assetID != 8 {
		t.Fatalf("balance %d %v", assetID, err)
	}
	if _, err := EncodeBalanceKey([]byte("alice"), 0); !errors.Is(err, domain.ErrInvalidAsset) {
		t.Fatal(err)
	}
	balPrefix, err := BalanceOwnerPrefix([]byte("alice"))
	if err != nil || !bytes.HasPrefix(balance, balPrefix) {
		t.Fatal("balance prefix")
	}

	nonceKey, err := EncodeAccountNonceKey([]byte("alice"))
	if err != nil {
		t.Fatal(err)
	}
	gotOwner, err = DecodeAccountNonceKey(nonceKey)
	if err != nil || string(gotOwner) != "alice" {
		t.Fatal(err)
	}

	marketSeq, err := EncodeMarketSequenceKey(11)
	if err != nil {
		t.Fatal(err)
	}
	tradeSeq, err := EncodeTradeSequenceKey(11)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(marketSeq, tradeSeq) || marketSeq[0] == tradeSeq[0] {
		t.Fatal("sequence keys collided")
	}
	if got, err := DecodeMarketSequenceKey(marketSeq); err != nil || got != 11 {
		t.Fatalf("market sequence %d %v", got, err)
	}
	if got, err := DecodeTradeSequenceKey(tradeSeq); err != nil || got != 11 {
		t.Fatalf("trade sequence %d %v", got, err)
	}
	if _, err := DecodeMarketSequenceKey(tradeSeq); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
	if _, err := EncodeMarketSequenceKey(0); !errors.Is(err, domain.ErrInvalidMarket) {
		t.Fatal(err)
	}

	rev := EncodeExchangeRevisionKey()
	if err := DecodeExchangeRevisionKey(rev); err != nil {
		t.Fatal(err)
	}
	if err := DecodeExchangeRevisionKey(append(rev, 1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}

	marketKey, err := EncodeMarketKey(11)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeMarketKey(marketKey); err != nil || got != 11 {
		t.Fatalf("market %d %v", got, err)
	}
	if _, err := EncodeMarketKey(0); !errors.Is(err, domain.ErrInvalidMarket) {
		t.Fatal(err)
	}
	tradeKey, err := EncodeTradeKey(11, 2)
	if err != nil {
		t.Fatal(err)
	}
	if gotMarket, gotSeq, err := DecodeTradeKey(tradeKey); err != nil || gotMarket != 11 || gotSeq != 2 {
		t.Fatalf("trade %d %d %v", gotMarket, gotSeq, err)
	}
	if _, err := EncodeTradeKey(11, 0); !errors.Is(err, domain.ErrInvalidSequence) {
		t.Fatal(err)
	}
	if bytes.Compare(marketKey, tradeKey) >= 0 {
		t.Fatal("market key is not before a later trade key of the same market")
	}

	first := []byte{
		active[0], open[0], client[0], exp1[0], balance[0], nonceKey[0], marketSeq[0], tradeSeq[0], rev[0],
		marketKey[0], tradeKey[0],
		PrefixAskBook, PrefixBidBook,
	}
	for i := range first {
		for j := i + 1; j < len(first); j++ {
			if first[i] == first[j] {
				t.Fatalf("duplicate prefix %x", first[i])
			}
		}
	}
}

func TestAssetKeys(t *testing.T) {
	key, err := EncodeAssetKey(4)
	if err != nil {
		t.Fatal(err)
	}
	if key[0] != PrefixAsset {
		t.Fatalf("prefix %x", key[0])
	}
	id, err := DecodeAssetKey(key)
	if err != nil || id != 4 {
		t.Fatalf("asset %d %v", id, err)
	}
	if _, err := EncodeAssetKey(0); !errors.Is(err, domain.ErrInvalidAsset) {
		t.Fatal(err)
	}
	denomKey, err := EncodeAssetDenomKey("base")
	if err != nil {
		t.Fatal(err)
	}
	if denomKey[0] != PrefixAssetDenom {
		t.Fatalf("denom prefix %x", denomKey[0])
	}
	denom, err := DecodeAssetDenomKey(denomKey)
	if err != nil || denom != "base" {
		t.Fatalf("denom %q %v", denom, err)
	}
	if _, err := EncodeAssetDenomKey(""); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
	if bytes.Compare(key, denomKey) >= 0 {
		t.Fatal("asset id key is not before the denom index")
	}
}

func assertStrictByteOrder(t *testing.T, keys [][]byte) {
	t.Helper()
	for i := 1; i < len(keys); i++ {
		if bytes.Compare(keys[i-1], keys[i]) >= 0 {
			t.Fatalf("key %d is not strictly before key %d", i-1, i)
		}
	}
}
