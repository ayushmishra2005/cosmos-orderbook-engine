package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestResultsHashStable(t *testing.T) {
	first, err := HashResults(resultFixture())
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashResults(resultFixture())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("identical results changed ResultsHash")
	}
	changed := resultFixture()
	changed[0].Remaining++
	other, err := HashResults(changed)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatal("a changed result did not change ResultsHash")
	}
	swapped := resultFixture()
	swapped[0], swapped[1] = swapped[1], swapped[0]
	reordered, err := HashResults(swapped)
	if err != nil {
		t.Fatal(err)
	}
	if reordered == first {
		t.Fatal("result order did not change ResultsHash")
	}
	trades := resultFixture()
	trades[1].Trades[0], trades[1].Trades[1] = trades[1].Trades[1], trades[1].Trades[0]
	tradeOrder, err := HashResults(trades)
	if err != nil {
		t.Fatal(err)
	}
	if tradeOrder == first {
		t.Fatal("trade order did not change ResultsHash")
	}
}

func TestResultsHashGolden(t *testing.T) {
	results := resultFixture()
	body0, err := encodeResultFixture(results[0])
	if err != nil {
		t.Fatal(err)
	}
	body1, err := encodeResultFixture(results[1])
	if err != nil {
		t.Fatal(err)
	}
	got0, err := EncodeCommandResult(results[0])
	if err != nil || !bytes.Equal(got0, body0) {
		t.Fatalf("result bytes\n got %x\nwant %x\nerr %v", got0, body0, err)
	}
	preimage := appendLenPrefixed(nil, []byte(BatchResultsDomain))
	preimage = appendU64(preimage, 2)
	preimage = appendLenPrefixed(preimage, body0)
	preimage = appendLenPrefixed(preimage, body1)
	sum := sha256.Sum256(preimage)
	got, err := HashResults(results)
	if err != nil || got != sum {
		t.Fatalf("results hash %x err %v", got, err)
	}
	const golden = "9a53d01300a9703001d69c5fa7f6a7beae87c43ef4d81bd3f2dba13d78c7fefb"
	if hex.EncodeToString(sum[:]) != golden {
		t.Fatalf("results golden %s", hex.EncodeToString(sum[:]))
	}
}

func TestBatchCommitmentChanges(t *testing.T) {
	base := commitmentFixture()
	got, err := HashBatchCommitment(base)
	if err != nil {
		t.Fatal(err)
	}
	again, err := HashBatchCommitment(commitmentFixture())
	if err != nil || again != got {
		t.Fatal("identical commitment input changed the digest")
	}
	cases := []struct {
		name string
		edit func(*BatchCommitmentInput)
	}{
		{"batch id", func(in *BatchCommitmentInput) { in.BatchID[0] ^= 0xff }},
		{"results hash", func(in *BatchCommitmentInput) { in.ResultsHash[0] ^= 0xff }},
		{"previous", func(in *BatchCommitmentInput) { in.PreviousBatchCommitment[0] ^= 0xff }},
		{"pre revision", func(in *BatchCommitmentInput) { in.PreExchangeRevision++ }},
		{"post revision", func(in *BatchCommitmentInput) { in.PostExchangeRevision++ }},
		{"height", func(in *BatchCommitmentInput) { in.ExecutionHeight++ }},
		{"chain", func(in *BatchCommitmentInput) { in.ChainID = "other-chain" }},
		{"instance", func(in *BatchCommitmentInput) { in.ExchangeInstanceID = []byte("other-instance") }},
	}
	for _, tc := range cases {
		next := base
		tc.edit(&next)
		other, err := HashBatchCommitment(next)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		if other == got {
			t.Fatalf("%s did not change the commitment", tc.name)
		}
	}
}

func TestBatchCommitmentGolden(t *testing.T) {
	in := commitmentFixture()
	in.BatchNumber = 1
	in.PreviousBatchCommitment = GenesisBatchCommitment
	if GenesisBatchCommitment != [32]byte{} {
		t.Fatal("genesis commitment is not 32 zero bytes")
	}
	preimage := appendLenPrefixed(nil, []byte(BatchCommitmentDomain))
	preimage = appendU32(preimage, BatchCommitmentVersion)
	preimage = appendLenPrefixed(preimage, []byte(in.ChainID))
	preimage = appendLenPrefixed(preimage, in.ExchangeInstanceID)
	preimage = appendU64(preimage, in.BatchNumber)
	preimage = append(preimage, in.BatchID[:]...)
	preimage = append(preimage, in.PreviousBatchCommitment[:]...)
	preimage = appendU64(preimage, in.ExecutionHeight)
	preimage = appendU64(preimage, in.PreExchangeRevision)
	preimage = appendU64(preimage, in.PostExchangeRevision)
	preimage = append(preimage, in.ResultsHash[:]...)
	sum := sha256.Sum256(preimage)
	got, err := HashBatchCommitment(in)
	if err != nil || got != sum {
		t.Fatalf("commitment %x err %v", got, err)
	}
	const golden = "bc047a82a19945152c0b61f32c16b3e8aeb654277442fc4e683f8855994cd785"
	if hex.EncodeToString(sum[:]) != golden {
		t.Fatalf("commitment golden %s", hex.EncodeToString(sum[:]))
	}
	next := in
	next.BatchNumber = 2
	next.PreviousBatchCommitment = sum
	next.PreExchangeRevision = in.PostExchangeRevision
	next.PostExchangeRevision = in.PostExchangeRevision + 1
	next.ResultsHash[0] ^= 0xff
	chained, err := HashBatchCommitment(next)
	if err != nil {
		t.Fatal(err)
	}
	if chained == sum {
		t.Fatal("second commitment matched the first")
	}
	if _, err := HashBatchCommitment(BatchCommitmentInput{Version: 2, ChainID: in.ChainID, ExchangeInstanceID: in.ExchangeInstanceID, BatchNumber: 1, BatchID: in.BatchID}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatal(err)
	}
}

func resultFixture() []Result {
	return []Result{
		{
			Index: 0, Type: CommandTypePlace, Owner: []byte{0x0a, 0x0b},
			OrderID: orderBytes(1), Status: ResultResting, Remaining: 5,
		},
		{
			Index: 1, Type: CommandTypePlace, Owner: bytes.Repeat([]byte{0x02}, 20),
			OrderID: orderBytes(2), Status: ResultFilled,
			Trades: []ResultTrade{{MarketID: 1, Sequence: 1}, {MarketID: 1, Sequence: 2}},
		},
	}
}

func commitmentFixture() BatchCommitmentInput {
	var id, prev, rh [32]byte
	for i := range id {
		id[i] = byte(i + 1)
		rh[i] = byte(0x80 + i)
	}
	prev[31] = 0x44
	return BatchCommitmentInput{
		Version:                 BatchCommitmentVersion,
		ChainID:                 "orderbook-1",
		ExchangeInstanceID:      []byte("orderbook-v1"),
		BatchNumber:             2,
		BatchID:                 id,
		PreviousBatchCommitment: prev,
		ExecutionHeight:         10,
		PreExchangeRevision:     4,
		PostExchangeRevision:    6,
		ResultsHash:             rh,
	}
}

func encodeResultFixture(r Result) ([]byte, error) {
	dst := appendU32(nil, r.Index)
	dst = append(dst, byte(r.Type))
	dst = appendLenPrefixed(dst, r.Owner)
	dst = append(dst, r.OrderID[:]...)
	dst = append(dst, r.Status)
	dst = appendU64(dst, r.Remaining)
	dst = appendU64(dst, uint64(len(r.Trades)))
	for _, trade := range r.Trades {
		dst = appendU64(dst, uint64(trade.MarketID))
		dst = appendU64(dst, trade.Sequence)
	}
	return dst, nil
}

func orderBytes(tag byte) domain.OrderID {
	var id domain.OrderID
	for i := range id {
		id[i] = tag
	}
	return id
}
