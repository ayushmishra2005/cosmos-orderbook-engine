package keeper

import (
	"context"

	"cosmossdk.io/core/store"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

// GetParams returns the authorized submitter and the latest batch number.
func (k Keeper) GetParams(ctx context.Context) (types.Params, error) {
	bz, err := k.get(ctx, types.ParamsKey())
	if err != nil {
		return types.Params{}, err
	}
	if bz == nil {
		return types.Params{}, types.ErrNotFound
	}
	return types.DecodeParams(bz)
}

// GetBatch loads one finalized batch by number.
func (k Keeper) GetBatch(ctx context.Context, number uint64) (types.Batch, error) {
	if number == 0 {
		return types.Batch{}, types.ErrNotFound
	}
	bz, err := k.get(ctx, types.BatchKey(number))
	if err != nil {
		return types.Batch{}, err
	}
	if bz == nil {
		return types.Batch{}, types.ErrNotFound
	}
	return types.DecodeBatch(bz)
}

// LatestBatch loads the highest finalized batch.
func (k Keeper) LatestBatch(ctx context.Context) (types.Batch, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return types.Batch{}, err
	}
	if params.Latest == 0 {
		return types.Batch{}, types.ErrNotFound
	}
	return k.GetBatch(ctx, params.Latest)
}

// GetBatchByID loads the batch bound to a batch ID.
func (k Keeper) GetBatchByID(ctx context.Context, id types.BatchID) (types.Batch, error) {
	if id.IsZero() {
		return types.Batch{}, types.ErrNotFound
	}
	bz, err := k.get(ctx, types.BatchIDKey(id))
	if err != nil {
		return types.Batch{}, err
	}
	if bz == nil {
		return types.Batch{}, types.ErrNotFound
	}
	number, err := types.DecodeUint64(bz)
	if err != nil {
		return types.Batch{}, err
	}
	batch, err := k.GetBatch(ctx, number)
	if err != nil {
		return types.Batch{}, err
	}
	if batch.ID != id {
		return types.Batch{}, types.ErrCorrupt
	}
	return batch, nil
}

func (k Keeper) storeBatch(ctx context.Context, batch types.Batch) error {
	existing, err := k.get(ctx, types.BatchIDKey(batch.ID))
	if err != nil {
		return err
	}
	if existing != nil {
		return types.ErrCorrupt
	}
	bz, err := types.EncodeBatch(batch)
	if err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	if err := kv.Set(types.BatchKey(batch.Number), bz); err != nil {
		return err
	}
	return kv.Set(types.BatchIDKey(batch.ID), types.EncodeUint64(batch.Number))
}

func (k Keeper) setParams(ctx context.Context, params types.Params) error {
	bz, err := types.EncodeParams(params)
	if err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	return kv.Set(types.ParamsKey(), bz)
}

func (k Keeper) kv(ctx context.Context) (store.KVStore, error) {
	return k.store.OpenKVStore(ctx), nil
}

func (k Keeper) get(ctx context.Context, key []byte) ([]byte, error) {
	kv, err := k.kv(ctx)
	if err != nil {
		return nil, err
	}
	bz, err := kv.Get(key)
	if err != nil || bz == nil {
		return bz, err
	}
	out := make([]byte, len(bz))
	copy(out, bz)
	return out, nil
}
