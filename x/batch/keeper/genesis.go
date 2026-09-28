package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

// InitGenesis writes the submitter, the latest number, and any imported records.
func (k Keeper) InitGenesis(ctx sdk.Context, gs v1.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	submitter, err := sdk.AccAddressFromBech32(gs.Submitter)
	if err != nil {
		return err
	}
	batches := make([]types.Batch, 0, len(gs.Batches))
	for _, pb := range gs.Batches {
		batch, err := v1.BatchFromProto(pb)
		if err != nil {
			return err
		}
		if err := k.verifyCommitment(batch); err != nil {
			return err
		}
		batches = append(batches, batch)
	}
	var head types.BatchCommitment
	if gs.LatestBatchNumber > 0 {
		copy(head[:], gs.LatestBatchCommitment)
	}
	return k.commit(ctx, func(ctx sdk.Context) error {
		if err := k.setParams(ctx, types.Params{
			Submitter: append([]byte(nil), submitter...),
			Latest:    gs.LatestBatchNumber,
			Head:      head,
		}); err != nil {
			return err
		}
		for _, batch := range batches {
			if err := k.storeBatch(ctx, batch); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExportGenesis reads the head and batch records in number order.
func (k Keeper) ExportGenesis(ctx sdk.Context) (v1.GenesisState, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return v1.GenesisState{}, err
	}
	gs := v1.GenesisState{
		Submitter:             sdk.AccAddress(params.Submitter).String(),
		LatestBatchNumber:     params.Latest,
		LatestBatchCommitment: append([]byte(nil), params.Head[:]...),
	}
	for number := uint64(1); number <= params.Latest; number++ {
		batch, err := k.GetBatch(ctx, number)
		if err != nil {
			return v1.GenesisState{}, err
		}
		pb, err := v1.BatchToProto(batch)
		if err != nil {
			return v1.GenesisState{}, err
		}
		gs.Batches = append(gs.Batches, pb)
	}
	return gs, nil
}
