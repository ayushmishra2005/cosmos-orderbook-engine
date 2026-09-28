package v1

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

func TestCommitmentProtoRoundTrip(t *testing.T) {
	owner := sdk.AccAddress(bytes.Repeat([]byte{0x03}, 20)).String()
	var id [32]byte
	id[0] = 9
	prev := bytes.Repeat([]byte{0x01}, 32)
	commitment := bytes.Repeat([]byte{0x02}, 32)
	results := bytes.Repeat([]byte{0x04}, 32)
	msg := &MsgFinalizeBatch{
		Submitter: owner, BatchNumber: 2, ExpectedExchangeRevision: 4,
		PreviousBatchCommitment: prev,
	}
	bz, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var back MsgFinalizeBatch
	if err := proto.Unmarshal(bz, &back); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.PreviousBatchCommitment, prev) || back.BatchNumber != 2 {
		t.Fatalf("%+v", back)
	}
	resp := &MsgFinalizeBatchResponse{BatchId: id[:], BatchCommitment: commitment, ResultsHash: results, PostExchangeRevision: 5}
	bz, err = proto.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var gotResp MsgFinalizeBatchResponse
	if err := proto.Unmarshal(bz, &gotResp); err != nil || !bytes.Equal(gotResp.BatchCommitment, commitment) || !bytes.Equal(gotResp.ResultsHash, results) {
		t.Fatalf("%+v %v", gotResp, err)
	}
	batch := &Batch{
		BatchNumber: 1, BatchId: id[:], ExecutionHeight: 8,
		PreExchangeRevision: 1, PostExchangeRevision: 2,
		PreviousBatchCommitment: make([]byte, 32),
		BatchCommitment:         commitment, ResultsHash: results,
		Results: []*CommandResult{{
			Index: 0, CommandType: CommandType_COMMAND_TYPE_PLACE_ORDER, Owner: owner,
			OrderId: id[:], Status: OrderStatus_ORDER_STATUS_RESTING, RemainingLots: 3,
		}},
	}
	bz, err = proto.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var gotBatch Batch
	if err := proto.Unmarshal(bz, &gotBatch); err != nil {
		t.Fatal(err)
	}
	decoded, err := BatchFromProto(&gotBatch)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Commitment != batchtypes.BatchCommitment(must32(commitment)) || decoded.ResultsHash != batchtypes.ResultsHash(must32(results)) || !decoded.Previous.IsZero() {
		t.Fatalf("%+v", decoded)
	}
	gs := &GenesisState{Submitter: owner, LatestBatchNumber: 0, LatestBatchCommitment: make([]byte, 32)}
	bz, err = proto.Marshal(gs)
	if err != nil {
		t.Fatal(err)
	}
	var gotGS GenesisState
	if err := proto.Unmarshal(bz, &gotGS); err != nil || !bytes.Equal(gotGS.LatestBatchCommitment, make([]byte, 32)) {
		t.Fatalf("%+v %v", gotGS, err)
	}
	q := &QueryBatchCommitmentResponse{
		BatchNumber: 2, BatchId: id[:], PreviousBatchCommitment: prev,
		BatchCommitment: commitment, ResultsHash: results,
	}
	bz, err = proto.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var gotQ QueryBatchCommitmentResponse
	if err := proto.Unmarshal(bz, &gotQ); err != nil || gotQ.BatchNumber != 2 || !bytes.Equal(gotQ.PreviousBatchCommitment, prev) || !bytes.Equal(gotQ.ResultsHash, results) {
		t.Fatalf("%+v %v", gotQ, err)
	}
}

func must32(b []byte) [32]byte {
	var out [32]byte
	copy(out[:], b)
	return out
}
