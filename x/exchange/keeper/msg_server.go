package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

type msgServer struct {
	k Keeper
}

// NewMsgServer returns the exchange message server.
func NewMsgServer(k Keeper) v1.MsgServer {
	return msgServer{k: k}
}

func (s msgServer) Deposit(ctx context.Context, msg *v1.MsgDeposit) (*v1.MsgDepositResponse, error) {
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return nil, err
	}
	if err := s.k.Deposit(ctx, owner, msg.Amount); err != nil {
		return nil, err
	}
	return &v1.MsgDepositResponse{}, nil
}

func (s msgServer) Withdraw(ctx context.Context, msg *v1.MsgWithdraw) (*v1.MsgWithdrawResponse, error) {
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return nil, err
	}
	if err := s.k.Withdraw(ctx, owner, msg.Amount); err != nil {
		return nil, err
	}
	return &v1.MsgWithdrawResponse{}, nil
}

func (s msgServer) PlaceOrder(ctx context.Context, msg *v1.MsgPlaceOrder) (*v1.MsgPlaceOrderResponse, error) {
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return nil, err
	}
	side, err := asSide(msg.Side)
	if err != nil {
		return nil, err
	}
	typ, err := asOrderType(msg.OrderType)
	if err != nil {
		return nil, err
	}
	tif, err := asTimeInForce(msg.TimeInForce)
	if err != nil {
		return nil, err
	}
	res, err := s.k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner:         owner,
		MarketID:      domain.MarketID(msg.MarketId),
		Side:          side,
		Type:          typ,
		TimeInForce:   tif,
		Price:         domain.Price(msg.PriceTicks),
		Quantity:      domain.Quantity(msg.QuantityLots),
		ExpiryHeight:  msg.ExpiryHeight,
		CommandNonce:  msg.CommandNonce,
		ClientOrderID: append([]byte(nil), msg.ClientOrderId...),
	})
	if err != nil {
		return nil, err
	}
	return &v1.MsgPlaceOrderResponse{
		OrderId:       append([]byte(nil), res.OrderID[:]...),
		RemainingLots: uint64(res.Remaining),
		Rested:        res.Rested,
	}, nil
}

func (s msgServer) CancelOrder(ctx context.Context, msg *v1.MsgCancelOrder) (*v1.MsgCancelOrderResponse, error) {
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return nil, err
	}
	id, err := orderIDFromBytes(msg.OrderId)
	if err != nil {
		return nil, err
	}
	res, err := s.k.CancelOrder(ctx, types.CancelOrderCommand{
		Owner:        owner,
		OrderID:      id,
		CommandNonce: msg.CommandNonce,
	})
	if err != nil {
		return nil, err
	}
	return &v1.MsgCancelOrderResponse{
		OrderId:  append([]byte(nil), res.OrderID[:]...),
		AssetId:  uint64(res.AssetID),
		Released: res.Released,
	}, nil
}

func asSide(v v1.Side) (domain.Side, error) {
	side := domain.Side(v)
	if !side.Valid() {
		return 0, domain.ErrInvalidSide
	}
	return side, nil
}

func asOrderType(v v1.OrderType) (domain.OrderType, error) {
	typ := domain.OrderType(v)
	if !typ.Valid() {
		return 0, domain.ErrInvalidOrderType
	}
	return typ, nil
}

func asTimeInForce(v v1.TimeInForce) (domain.TimeInForce, error) {
	tif := domain.TimeInForce(v)
	if !tif.Valid() {
		return 0, domain.ErrInvalidTimeInForce
	}
	return tif, nil
}

func orderIDFromBytes(bz []byte) (domain.OrderID, error) {
	if len(bz) != len(domain.OrderID{}) {
		return domain.OrderID{}, domain.ErrInvalidOrderID
	}
	var id domain.OrderID
	copy(id[:], bz)
	if id.IsZero() {
		return domain.OrderID{}, domain.ErrInvalidOrderID
	}
	return id, nil
}
