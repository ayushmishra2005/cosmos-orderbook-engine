package v1

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

// BatchFromProto converts a query or genesis batch into the store record.
func BatchFromProto(pb *Batch) (batchtypes.Batch, error) {
	if pb == nil || len(pb.BatchId) != len(batchtypes.BatchID{}) {
		return batchtypes.Batch{}, batchtypes.ErrCorrupt
	}
	var id batchtypes.BatchID
	copy(id[:], pb.BatchId)
	results := make([]batchtypes.CommandResult, 0, len(pb.Results))
	for _, result := range pb.Results {
		decoded, err := resultFromProto(result)
		if err != nil {
			return batchtypes.Batch{}, err
		}
		results = append(results, decoded)
	}
	return batchtypes.Batch{
		Number:       pb.BatchNumber,
		ID:           id,
		Height:       pb.ExecutionHeight,
		PreRevision:  pb.PreExchangeRevision,
		PostRevision: pb.PostExchangeRevision,
		Results:      results,
	}, nil
}

func resultFromProto(pb *CommandResult) (batchtypes.CommandResult, error) {
	if pb == nil || len(pb.OrderId) != len(domain.OrderID{}) {
		return batchtypes.CommandResult{}, batchtypes.ErrCorrupt
	}
	owner, err := sdk.AccAddressFromBech32(pb.Owner)
	if err != nil {
		return batchtypes.CommandResult{}, err
	}
	typ, err := commandTypeFromProto(pb.CommandType)
	if err != nil {
		return batchtypes.CommandResult{}, err
	}
	status, err := statusFromProto(pb.Status)
	if err != nil {
		return batchtypes.CommandResult{}, err
	}
	trades := make([]batchtypes.TradeRef, 0, len(pb.Trades))
	for _, trade := range pb.Trades {
		if trade == nil {
			return batchtypes.CommandResult{}, batchtypes.ErrCorrupt
		}
		trades = append(trades, batchtypes.TradeRef{
			MarketID: domain.MarketID(trade.MarketId),
			Sequence: trade.Sequence,
		})
	}
	var id domain.OrderID
	copy(id[:], pb.OrderId)
	return batchtypes.CommandResult{
		Index:     pb.Index,
		Type:      typ,
		Owner:     append([]byte(nil), owner...),
		OrderID:   id,
		Status:    status,
		Remaining: pb.RemainingLots,
		Trades:    trades,
	}, nil
}

// BatchToProto converts a store record into the query form.
func BatchToProto(b batchtypes.Batch) (*Batch, error) {
	results := make([]*CommandResult, 0, len(b.Results))
	for _, result := range b.Results {
		pb, err := resultToProto(result)
		if err != nil {
			return nil, err
		}
		results = append(results, pb)
	}
	return &Batch{
		BatchNumber:          b.Number,
		BatchId:              append([]byte(nil), b.ID[:]...),
		ExecutionHeight:      b.Height,
		PreExchangeRevision:  b.PreRevision,
		PostExchangeRevision: b.PostRevision,
		Results:              results,
	}, nil
}

func resultToProto(r batchtypes.CommandResult) (*CommandResult, error) {
	typ, err := commandTypeToProto(r.Type)
	if err != nil {
		return nil, err
	}
	status, err := statusToProto(r.Status)
	if err != nil {
		return nil, err
	}
	trades := make([]*TradeRef, 0, len(r.Trades))
	for _, trade := range r.Trades {
		trades = append(trades, &TradeRef{MarketId: uint64(trade.MarketID), Sequence: trade.Sequence})
	}
	return &CommandResult{
		Index:         r.Index,
		CommandType:   typ,
		Owner:         sdk.AccAddress(r.Owner).String(),
		OrderId:       append([]byte(nil), r.OrderID[:]...),
		Status:        status,
		RemainingLots: r.Remaining,
		Trades:        trades,
	}, nil
}

func commandTypeFromProto(v CommandType) (byte, error) {
	switch v {
	case CommandType_COMMAND_TYPE_PLACE_ORDER:
		return batchtypes.CommandPlace, nil
	case CommandType_COMMAND_TYPE_CANCEL_ORDER:
		return batchtypes.CommandCancel, nil
	default:
		return 0, batchtypes.ErrCorrupt
	}
}

func commandTypeToProto(v byte) (CommandType, error) {
	switch v {
	case batchtypes.CommandPlace:
		return CommandType_COMMAND_TYPE_PLACE_ORDER, nil
	case batchtypes.CommandCancel:
		return CommandType_COMMAND_TYPE_CANCEL_ORDER, nil
	default:
		return 0, batchtypes.ErrCorrupt
	}
}

func statusFromProto(v OrderStatus) (byte, error) {
	switch v {
	case OrderStatus_ORDER_STATUS_RESTING:
		return batchtypes.StatusResting, nil
	case OrderStatus_ORDER_STATUS_FILLED:
		return batchtypes.StatusFilled, nil
	case OrderStatus_ORDER_STATUS_CANCELLED:
		return batchtypes.StatusCancelled, nil
	case OrderStatus_ORDER_STATUS_UNFILLED:
		return batchtypes.StatusUnfilled, nil
	default:
		return 0, batchtypes.ErrCorrupt
	}
}

func statusToProto(v byte) (OrderStatus, error) {
	switch v {
	case batchtypes.StatusResting:
		return OrderStatus_ORDER_STATUS_RESTING, nil
	case batchtypes.StatusFilled:
		return OrderStatus_ORDER_STATUS_FILLED, nil
	case batchtypes.StatusCancelled:
		return OrderStatus_ORDER_STATUS_CANCELLED, nil
	case batchtypes.StatusUnfilled:
		return OrderStatus_ORDER_STATUS_UNFILLED, nil
	default:
		return 0, batchtypes.ErrCorrupt
	}
}
