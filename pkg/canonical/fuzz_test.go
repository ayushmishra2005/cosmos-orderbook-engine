package canonical

import (
	"bytes"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func FuzzAskBidKey(f *testing.F) {
	f.Add(uint64(1), uint64(1), uint64(1))
	f.Add(uint64(mathMax), uint64(mathMax), uint64(mathMax))
	f.Fuzz(func(t *testing.T, market, price, sequence uint64) {
		ask, askErr := EncodeAskKey(domain.MarketID(market), domain.Price(price), domain.Sequence(sequence))
		bid, bidErr := EncodeBidKey(domain.MarketID(market), domain.Price(price), domain.Sequence(sequence))
		if (askErr == nil) != (bidErr == nil) {
			t.Fatalf("ask %v bid %v", askErr, bidErr)
		}
		if askErr != nil {
			return
		}
		if bytes.Equal(ask, bid) {
			t.Fatal("ask and bid collided")
		}
		gotM, gotP, gotS, err := DecodeAskKey(ask)
		if err != nil || gotM != domain.MarketID(market) || gotP != domain.Price(price) || gotS != domain.Sequence(sequence) {
			t.Fatalf("ask round trip %d %d %d %v", gotM, gotP, gotS, err)
		}
		gotM, gotP, gotS, err = DecodeBidKey(bid)
		if err != nil || gotM != domain.MarketID(market) || gotP != domain.Price(price) || gotS != domain.Sequence(sequence) {
			t.Fatalf("bid round trip %d %d %d %v", gotM, gotP, gotS, err)
		}
		if _, _, _, err := DecodeAskKey(bid); err == nil {
			t.Fatal("cross decode")
		}
	})
}

func FuzzOrderID(f *testing.F) {
	f.Add("testing", []byte{1, 2, 3, 4}, []byte{9, 8, 7}, uint64(1), uint64(1))
	f.Fuzz(func(t *testing.T, chain string, instance, owner []byte, market, nonce uint64) {
		in := OrderIDInput{
			ChainID:            chain,
			ExchangeInstanceID: append([]byte(nil), instance...),
			Owner:              append([]byte(nil), owner...),
			MarketID:           domain.MarketID(market),
			CommandNonce:       nonce,
		}
		a, errA := HashOrderID(in)
		b, errB := HashOrderID(in)
		if (errA == nil) != (errB == nil) || a != b {
			t.Fatalf("unstable %s %v / %s %v", a, errA, b, errB)
		}
	})
}

const mathMax = ^uint64(0)
