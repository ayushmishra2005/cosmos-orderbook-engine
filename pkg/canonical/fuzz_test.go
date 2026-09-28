package canonical

import (
	"bytes"
	"encoding/binary"
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

func FuzzAskKeyOrder(f *testing.F) {
	f.Add(uint64(1), uint64(1), uint64(1), uint64(2), uint64(1))
	f.Fuzz(func(t *testing.T, market, p1, s1, p2, s2 uint64) {
		if market == 0 || p1 == 0 || p2 == 0 || s1 == 0 || s2 == 0 {
			return
		}
		a, err := EncodeAskKey(domain.MarketID(market), domain.Price(p1), domain.Sequence(s1))
		if err != nil {
			t.Fatal(err)
		}
		b, err := EncodeAskKey(domain.MarketID(market), domain.Price(p2), domain.Sequence(s2))
		if err != nil {
			t.Fatal(err)
		}
		cmp := bytes.Compare(a, b)
		switch {
		case p1 < p2 && cmp >= 0:
			t.Fatal("lower price sorted after")
		case p1 > p2 && cmp <= 0:
			t.Fatal("higher price sorted before")
		case p1 == p2 && s1 < s2 && cmp >= 0:
			t.Fatal("older sequence sorted after")
		case p1 == p2 && s1 > s2 && cmp <= 0:
			t.Fatal("newer sequence sorted before")
		case p1 == p2 && s1 == s2 && cmp != 0:
			t.Fatal("equal keys differed")
		}
	})
}

func FuzzHashResults(f *testing.F) {
	f.Add(uint32(0), uint8(0), uint8(0), uint64(1), uint64(1), uint64(1))
	f.Fuzz(func(t *testing.T, index uint32, typ, status uint8, remaining, market, seq uint64) {
		var id domain.OrderID
		binary.BigEndian.PutUint64(id[24:], 1)
		r := Result{
			Index:     index,
			Type:      CommandType(typ%2 + 1),
			Owner:     bytes.Repeat([]byte{0x11}, 20),
			OrderID:   id,
			Status:    status%4 + 1,
			Remaining: remaining,
		}
		if market != 0 && seq != 0 {
			r.Trades = []ResultTrade{{MarketID: domain.MarketID(market), Sequence: seq}}
		}
		a, errA := HashResults([]Result{r})
		b, errB := HashResults([]Result{r})
		if errA != nil || errB != nil || a != b {
			t.Fatalf("unstable %v %v", errA, errB)
		}
		changed := r
		changed.Remaining ^= 1
		c, errC := HashResults([]Result{changed})
		if errC != nil || c == a {
			t.Fatal("remaining was not committed")
		}
	})
}

func FuzzBatchCommitment(f *testing.F) {
	f.Add("orderbook-test", []byte("orderbook-v1"), uint64(1), uint64(10), uint64(0), uint64(2))
	f.Fuzz(func(t *testing.T, chain string, instance []byte, batch, height, pre, post uint64) {
		if chain == "" || len(chain) > maxChainIDLen || len(instance) == 0 || len(instance) > maxInstanceIDLen || batch == 0 {
			return
		}
		var id, prev, results [32]byte
		id[0] = 1
		results[0] = 2
		binary.BigEndian.PutUint64(prev[:], pre)
		in := BatchCommitmentInput{
			Version:                 BatchCommitmentVersion,
			ChainID:                 chain,
			ExchangeInstanceID:      append([]byte(nil), instance...),
			BatchNumber:             batch,
			BatchID:                 id,
			PreviousBatchCommitment: prev,
			ExecutionHeight:         height,
			PreExchangeRevision:     pre,
			PostExchangeRevision:    post,
			ResultsHash:             results,
		}
		a, errA := HashBatchCommitment(in)
		b, errB := HashBatchCommitment(in)
		if errA != nil || errB != nil || a != b {
			t.Fatalf("unstable %v %v", errA, errB)
		}
		in.PreviousBatchCommitment[0] ^= 0xff
		c, errC := HashBatchCommitment(in)
		if errC != nil || c == a {
			t.Fatal("previous commitment was not committed")
		}
	})
}

const mathMax = ^uint64(0)
