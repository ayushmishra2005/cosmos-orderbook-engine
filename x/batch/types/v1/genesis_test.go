package v1

import (
	"bytes"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

func TestBatchGenesisRejectsBadHead(t *testing.T) {
	owner := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)).String()
	var id, commit, results [32]byte
	id[0] = 1
	commit[0] = 2
	results[0] = 3
	batch := &Batch{
		BatchNumber:             1,
		BatchId:                 id[:],
		ExecutionHeight:         1,
		PreExchangeRevision:     1,
		PostExchangeRevision:    2,
		PreviousBatchCommitment: make([]byte, 32),
		BatchCommitment:         commit[:],
		ResultsHash:             results[:],
		Results: []*CommandResult{{
			Index:         0,
			CommandType:   CommandType_COMMAND_TYPE_PLACE_ORDER,
			Owner:         owner,
			OrderId:       bytes.Repeat([]byte{0x02}, 32),
			Status:        OrderStatus_ORDER_STATUS_RESTING,
			RemainingLots: 1,
		}},
	}
	gs := GenesisState{
		Submitter:             owner,
		LatestBatchNumber:     1,
		LatestBatchCommitment: append([]byte(nil), commit[:]...),
		Batches:               []*Batch{batch},
	}
	if err := gs.Validate(); err != nil {
		t.Fatal(err)
	}
	gs.LatestBatchCommitment = bytes.Repeat([]byte{9}, 32)
	if err := gs.Validate(); !errors.Is(err, batchtypes.ErrCorrupt) {
		t.Fatalf("inconsistent head: %v", err)
	}
	gs.LatestBatchCommitment = []byte{1, 2, 3}
	if err := gs.Validate(); !errors.Is(err, batchtypes.ErrCorrupt) {
		t.Fatalf("malformed head: %v", err)
	}
	gs.LatestBatchNumber = 2
	gs.LatestBatchCommitment = append([]byte(nil), commit[:]...)
	if err := gs.Validate(); !errors.Is(err, batchtypes.ErrCorrupt) {
		t.Fatalf("batch count: %v", err)
	}
	empty := GenesisState{Submitter: owner, LatestBatchCommitment: bytes.Repeat([]byte{1}, 32)}
	if err := empty.Validate(); !errors.Is(err, batchtypes.ErrCorrupt) {
		t.Fatalf("nonzero head before batch 1: %v", err)
	}
}
