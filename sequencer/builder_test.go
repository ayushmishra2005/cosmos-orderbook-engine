package sequencer

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestBuildUsesChainHeadAndKeepsOrder(t *testing.T) {
	cfg := testConfig(t)
	alice := newKey()
	bob := newKey()
	sell := signPlace(t, alice, cfg, 1, domain.SideSell)
	buy := signPlace(t, bob, cfg, 1, domain.SideBuy)
	var previous [32]byte
	previous[0] = 0xab
	head := Head{Latest: 4, Previous: previous, Revision: 9}

	msg, id, err := BuildFinalize(cfg.Submitter, head, []canonical.Command{sell, buy})
	if err != nil {
		t.Fatal(err)
	}
	if msg.BatchNumber != 5 {
		t.Fatalf("batch number %d", msg.BatchNumber)
	}
	if msg.ExpectedExchangeRevision != 9 {
		t.Fatalf("revision %d", msg.ExpectedExchangeRevision)
	}
	if !bytes.Equal(msg.PreviousBatchCommitment, previous[:]) {
		t.Fatalf("previous commitment %x", msg.PreviousBatchCommitment)
	}
	if msg.Commands[0].Owner != sdk.AccAddress(sell.Owner).String() || msg.Commands[1].Owner != sdk.AccAddress(buy.Owner).String() {
		t.Fatal("command order changed")
	}
	if msg.Commands[0].CommandNonce != 1 || msg.Commands[1].CommandNonce != 1 {
		t.Fatal("nonce order changed")
	}
	want, err := canonical.HashBatchID(5, 9, []canonical.Command{sell, buy})
	if err != nil {
		t.Fatal(err)
	}
	if id != want {
		t.Fatalf("sequencer batch id %x, canonical %x", id, want)
	}
	decoded, err := commandsFromMsg(msg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := canonical.HashBatchID(5, 9, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if again != want {
		t.Fatal("protobuf round trip changed the batch id")
	}
	previous[0] = 0
	if msg.PreviousBatchCommitment[0] != 0xab {
		t.Fatal("batch aliased the head commitment")
	}
}

func TestBuildBatchOneZeroPrevious(t *testing.T) {
	cfg := testConfig(t)
	cmd := signPlace(t, newKey(), cfg, 1, domain.SideBuy)
	var previous [32]byte
	previous[1] = 1
	if _, _, err := BuildFinalize(cfg.Submitter, Head{Previous: previous}, []canonical.Command{cmd}); err == nil {
		t.Fatal("expected batch 1 to reject a non-zero previous commitment")
	}
}
