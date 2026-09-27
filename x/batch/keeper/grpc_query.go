package keeper

import (
	"context"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

type queryServer struct {
	k Keeper
}

// NewQueryServer returns the batch query server.
func NewQueryServer(k Keeper) v1.QueryServer {
	return queryServer{k: k}
}

func (q queryServer) Batch(ctx context.Context, req *v1.QueryBatchRequest) (*v1.QueryBatchResponse, error) {
	if req == nil {
		return nil, types.ErrNotFound
	}
	batch, err := q.k.GetBatch(ctx, req.BatchNumber)
	if err != nil {
		return nil, err
	}
	pb, err := v1.BatchToProto(batch)
	if err != nil {
		return nil, err
	}
	return &v1.QueryBatchResponse{Batch: pb}, nil
}

func (q queryServer) LatestBatch(ctx context.Context, req *v1.QueryLatestBatchRequest) (*v1.QueryLatestBatchResponse, error) {
	if req == nil {
		return nil, types.ErrNotFound
	}
	batch, err := q.k.LatestBatch(ctx)
	if err != nil {
		return nil, err
	}
	pb, err := v1.BatchToProto(batch)
	if err != nil {
		return nil, err
	}
	return &v1.QueryLatestBatchResponse{Batch: pb}, nil
}

func (q queryServer) BatchByID(ctx context.Context, req *v1.QueryBatchByIDRequest) (*v1.QueryBatchByIDResponse, error) {
	if req == nil || len(req.BatchId) != len(types.BatchID{}) {
		return nil, types.ErrNotFound
	}
	var id types.BatchID
	copy(id[:], req.BatchId)
	batch, err := q.k.GetBatchByID(ctx, id)
	if err != nil {
		return nil, err
	}
	pb, err := v1.BatchToProto(batch)
	if err != nil {
		return nil, err
	}
	return &v1.QueryBatchByIDResponse{Batch: pb}, nil
}
