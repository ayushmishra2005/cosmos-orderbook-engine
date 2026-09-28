package keeper

import (
	"bytes"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func FuzzBookIteratorBound(f *testing.F) {
	f.Add(uint64(1), uint64(1), uint64(1))
	f.Add(uint64(1<<32), uint64(9), uint64(4))
	f.Fuzz(func(t *testing.T, market, price, sequence uint64) {
		prefix, err := canonical.AskMarketPrefix(domain.MarketID(market))
		if err != nil {
			return
		}
		key, err := canonical.EncodeAskKey(domain.MarketID(market), domain.Price(price), domain.Sequence(sequence))
		if err != nil {
			return
		}
		if !bytes.HasPrefix(key, prefix) {
			t.Fatal("ask key lost its prefix")
		}
		end := prefixEnd(prefix)
		if end != nil && bytes.Compare(key, end) >= 0 {
			t.Fatal("ask key is not inside the market iterator")
		}
		bidPrefix, err := canonical.BidMarketPrefix(domain.MarketID(market))
		if err != nil {
			t.Fatal(err)
		}
		bid, err := canonical.EncodeBidKey(domain.MarketID(market), domain.Price(price), domain.Sequence(sequence))
		if err != nil {
			t.Fatal(err)
		}
		bidEnd := prefixEnd(bidPrefix)
		if bidEnd != nil && bytes.Compare(bid, bidEnd) >= 0 {
			t.Fatal("bid key is not inside the market iterator")
		}
	})
}
