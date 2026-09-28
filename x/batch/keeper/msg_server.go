package keeper

import (
	"context"

	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

type msgServer struct {
	k Keeper
}

// NewMsgServer returns the batch message server.
func NewMsgServer(k Keeper) v1.MsgServer {
	return msgServer{k: k}
}

func (s msgServer) FinalizeBatch(ctx context.Context, msg *v1.MsgFinalizeBatch) (*v1.MsgFinalizeBatchResponse, error) {
	batch, err := s.k.FinalizeBatch(ctx, msg)
	if err != nil {
		return nil, err
	}
	pb, err := v1.BatchToProto(batch)
	if err != nil {
		return nil, err
	}
	return &v1.MsgFinalizeBatchResponse{
		BatchId:              append([]byte(nil), batch.ID[:]...),
		PostExchangeRevision: batch.PostRevision,
		Results:              pb.Results,
		BatchCommitment:      append([]byte(nil), batch.Commitment[:]...),
		ResultsHash:          append([]byte(nil), batch.ResultsHash[:]...),
	}, nil
}
