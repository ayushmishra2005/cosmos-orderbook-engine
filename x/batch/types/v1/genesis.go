package v1

import (
	"bytes"

	sdk "github.com/cosmos/cosmos-sdk/types"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

// DefaultSubmitter is the genesis submitter until an operator replaces it.
// Batch submission is authorized for this one address.
func DefaultSubmitter() sdk.AccAddress {
	return bytes.Repeat([]byte{0x01}, 20)
}

// DefaultGenesis is an empty batch history with the placeholder submitter.
func DefaultGenesis() *GenesisState {
	return &GenesisState{Submitter: DefaultSubmitter().String()}
}

// Validate checks the submitter and that batch records are 1..latest.
func (gs GenesisState) Validate() error {
	if _, err := sdk.AccAddressFromBech32(gs.Submitter); err != nil {
		return err
	}
	if uint64(len(gs.Batches)) != gs.LatestBatchNumber {
		return batchtypes.ErrCorrupt
	}
	var zero batchtypes.BatchCommitment
	if gs.LatestBatchNumber == 0 {
		if len(gs.LatestBatchCommitment) != 0 && !bytes.Equal(gs.LatestBatchCommitment, zero[:]) {
			return batchtypes.ErrCorrupt
		}
	} else if len(gs.LatestBatchCommitment) != len(zero) {
		return batchtypes.ErrCorrupt
	}
	seen := make(map[batchtypes.BatchID]struct{}, len(gs.Batches))
	var head batchtypes.BatchCommitment
	for i, pb := range gs.Batches {
		batch, err := BatchFromProto(pb)
		if err != nil {
			return err
		}
		if batch.Number != uint64(i)+1 {
			return batchtypes.ErrBatchNumber
		}
		if err := batch.Validate(); err != nil {
			return err
		}
		if batch.Previous != head {
			return batchtypes.ErrPreviousCommitment
		}
		if _, ok := seen[batch.ID]; ok {
			return batchtypes.ErrCorrupt
		}
		seen[batch.ID] = struct{}{}
		head = batch.Commitment
	}
	if gs.LatestBatchNumber > 0 && !bytes.Equal(gs.LatestBatchCommitment, head[:]) {
		return batchtypes.ErrCorrupt
	}
	return nil
}
