package sequencer

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

// Head is the chain state a batch is built against.
// Latest is 0 when no batch has been finalized. Previous is then 32 zero bytes.
type Head struct {
	Latest   uint64
	Previous [32]byte
	Revision uint64
}

// BuildFinalize freezes cmds in the given order and builds MsgFinalizeBatch.
// The returned BatchID is the digest validators derive from that message.
func BuildFinalize(submitter string, head Head, cmds []canonical.Command) (*batchv1.MsgFinalizeBatch, [32]byte, error) {
	if submitter == "" {
		return nil, [32]byte{}, ErrMalformed
	}
	if _, err := sdk.AccAddressFromBech32(submitter); err != nil {
		return nil, [32]byte{}, fmt.Errorf("%w: submitter", ErrMalformed)
	}
	if len(cmds) == 0 {
		return nil, [32]byte{}, ErrEmptyBatch
	}
	if head.Latest == ^uint64(0) {
		return nil, [32]byte{}, fmt.Errorf("%w: batch number", ErrHead)
	}
	if head.Latest == 0 && head.Previous != ([32]byte{}) {
		return nil, [32]byte{}, fmt.Errorf("%w: batch 1 previous commitment", ErrHead)
	}
	frozen := make([]canonical.Command, len(cmds))
	for i := range cmds {
		frozen[i] = cloneCommand(cmds[i])
	}
	number := head.Latest + 1
	id, err := canonical.HashBatchID(number, head.Revision, frozen)
	if err != nil {
		return nil, [32]byte{}, err
	}
	pb := make([]*batchv1.SignedCommand, len(frozen))
	for i := range frozen {
		cmd, err := toProto(frozen[i])
		if err != nil {
			return nil, [32]byte{}, err
		}
		pb[i] = cmd
	}
	msg := &batchv1.MsgFinalizeBatch{
		Submitter:                submitter,
		BatchNumber:              number,
		ExpectedExchangeRevision: head.Revision,
		PreviousBatchCommitment:  append([]byte(nil), head.Previous[:]...),
		Commands:                 pb,
	}
	decoded, err := commandsFromMsg(msg)
	if err != nil {
		return nil, [32]byte{}, err
	}
	again, err := canonical.HashBatchID(number, head.Revision, decoded)
	if err != nil {
		return nil, [32]byte{}, err
	}
	if again != id {
		return nil, [32]byte{}, fmt.Errorf("%w: batch id changed across protobuf", ErrHead)
	}
	return msg, id, nil
}

func toProto(cmd canonical.Command) (*batchv1.SignedCommand, error) {
	pb := &batchv1.SignedCommand{
		ProtocolVersion:    cmd.ProtocolVersion,
		ChainId:            cmd.ChainID,
		ExchangeInstanceId: append([]byte(nil), cmd.ExchangeInstanceID...),
		Owner:              sdk.AccAddress(cmd.Owner).String(),
		CommandNonce:       cmd.Nonce,
		PubKey:             append([]byte(nil), cmd.PubKey...),
		Signature:          append([]byte(nil), cmd.Signature...),
	}
	switch cmd.Type {
	case canonical.CommandTypePlace:
		if cmd.Place == nil || cmd.Cancel != nil {
			return nil, ErrMalformed
		}
		side, err := sideToProto(cmd.Place.Side)
		if err != nil {
			return nil, err
		}
		orderType, err := orderTypeToProto(cmd.Place.Type)
		if err != nil {
			return nil, err
		}
		tif, err := tifToProto(cmd.Place.TimeInForce)
		if err != nil {
			return nil, err
		}
		pb.CommandType = batchv1.CommandType_COMMAND_TYPE_PLACE_ORDER
		pb.Place = &batchv1.Place{
			MarketId:      uint64(cmd.Place.MarketID),
			Side:          side,
			OrderType:     orderType,
			TimeInForce:   tif,
			QuantityLots:  uint64(cmd.Place.Quantity),
			PriceTicks:    uint64(cmd.Place.Price),
			ExpiryHeight:  cmd.Place.ExpiryHeight,
			ClientOrderId: append([]byte(nil), cmd.Place.ClientOrderID...),
		}
	case canonical.CommandTypeCancel:
		if cmd.Cancel == nil || cmd.Place != nil {
			return nil, ErrMalformed
		}
		pb.CommandType = batchv1.CommandType_COMMAND_TYPE_CANCEL_ORDER
		pb.Cancel = &batchv1.Cancel{OrderId: append([]byte(nil), cmd.Cancel.OrderID[:]...)}
	default:
		return nil, ErrUnsupportedCommand
	}
	return pb, nil
}

func commandsFromMsg(msg *batchv1.MsgFinalizeBatch) ([]canonical.Command, error) {
	if msg == nil {
		return nil, ErrMalformed
	}
	out := make([]canonical.Command, len(msg.Commands))
	for i, pb := range msg.Commands {
		cmd, err := fromProto(pb)
		if err != nil {
			return nil, err
		}
		out[i] = cmd
	}
	return out, nil
}

func fromProto(pb *batchv1.SignedCommand) (canonical.Command, error) {
	if pb == nil {
		return canonical.Command{}, ErrMalformed
	}
	owner, err := sdk.AccAddressFromBech32(pb.Owner)
	if err != nil {
		return canonical.Command{}, ErrMalformed
	}
	cmd := canonical.Command{
		ProtocolVersion:    pb.ProtocolVersion,
		ChainID:            pb.ChainId,
		ExchangeInstanceID: append([]byte(nil), pb.ExchangeInstanceId...),
		Owner:              append([]byte(nil), owner...),
		Nonce:              pb.CommandNonce,
		PubKey:             append([]byte(nil), pb.PubKey...),
		Signature:          append([]byte(nil), pb.Signature...),
	}
	switch pb.CommandType {
	case batchv1.CommandType_COMMAND_TYPE_PLACE_ORDER:
		if pb.Place == nil || pb.Cancel != nil {
			return canonical.Command{}, ErrMalformed
		}
		side, err := sideFromProto(pb.Place.Side)
		if err != nil {
			return canonical.Command{}, err
		}
		orderType, err := orderTypeFromProto(pb.Place.OrderType)
		if err != nil {
			return canonical.Command{}, err
		}
		tif, err := tifFromProto(pb.Place.TimeInForce)
		if err != nil {
			return canonical.Command{}, err
		}
		cmd.Type = canonical.CommandTypePlace
		cmd.Place = &canonical.Place{
			MarketID:      domain.MarketID(pb.Place.MarketId),
			Side:          side,
			Type:          orderType,
			TimeInForce:   tif,
			Quantity:      domain.Quantity(pb.Place.QuantityLots),
			Price:         domain.Price(pb.Place.PriceTicks),
			ExpiryHeight:  pb.Place.ExpiryHeight,
			ClientOrderID: append([]byte(nil), pb.Place.ClientOrderId...),
		}
	case batchv1.CommandType_COMMAND_TYPE_CANCEL_ORDER:
		if pb.Cancel == nil || pb.Place != nil || len(pb.Cancel.OrderId) != len(domain.OrderID{}) {
			return canonical.Command{}, ErrMalformed
		}
		var id domain.OrderID
		copy(id[:], pb.Cancel.OrderId)
		cmd.Type = canonical.CommandTypeCancel
		cmd.Cancel = &canonical.Cancel{OrderID: id}
	default:
		return canonical.Command{}, ErrUnsupportedCommand
	}
	return cmd, nil
}

func sideToProto(s domain.Side) (batchv1.Side, error) {
	switch s {
	case domain.SideBuy:
		return batchv1.Side_SIDE_BUY, nil
	case domain.SideSell:
		return batchv1.Side_SIDE_SELL, nil
	default:
		return 0, ErrMalformed
	}
}

func sideFromProto(s batchv1.Side) (domain.Side, error) {
	switch s {
	case batchv1.Side_SIDE_BUY:
		return domain.SideBuy, nil
	case batchv1.Side_SIDE_SELL:
		return domain.SideSell, nil
	default:
		return 0, ErrMalformed
	}
}

func orderTypeToProto(t domain.OrderType) (batchv1.OrderType, error) {
	switch t {
	case domain.OrderTypeLimit:
		return batchv1.OrderType_ORDER_TYPE_LIMIT, nil
	case domain.OrderTypeMarket:
		return batchv1.OrderType_ORDER_TYPE_MARKET, nil
	default:
		return 0, ErrMalformed
	}
}

func orderTypeFromProto(t batchv1.OrderType) (domain.OrderType, error) {
	switch t {
	case batchv1.OrderType_ORDER_TYPE_LIMIT:
		return domain.OrderTypeLimit, nil
	case batchv1.OrderType_ORDER_TYPE_MARKET:
		return domain.OrderTypeMarket, nil
	default:
		return 0, ErrMalformed
	}
}

func tifToProto(t domain.TimeInForce) (batchv1.TimeInForce, error) {
	switch t {
	case domain.TimeInForceGTC:
		return batchv1.TimeInForce_TIME_IN_FORCE_GTC, nil
	case domain.TimeInForceIOC:
		return batchv1.TimeInForce_TIME_IN_FORCE_IOC, nil
	case domain.TimeInForceFOK:
		return batchv1.TimeInForce_TIME_IN_FORCE_FOK, nil
	case domain.TimeInForceGTD:
		return batchv1.TimeInForce_TIME_IN_FORCE_GTD, nil
	default:
		return 0, ErrMalformed
	}
}

func tifFromProto(t batchv1.TimeInForce) (domain.TimeInForce, error) {
	switch t {
	case batchv1.TimeInForce_TIME_IN_FORCE_GTC:
		return domain.TimeInForceGTC, nil
	case batchv1.TimeInForce_TIME_IN_FORCE_IOC:
		return domain.TimeInForceIOC, nil
	case batchv1.TimeInForce_TIME_IN_FORCE_FOK:
		return domain.TimeInForceFOK, nil
	case batchv1.TimeInForce_TIME_IN_FORCE_GTD:
		return domain.TimeInForceGTD, nil
	default:
		return 0, ErrMalformed
	}
}
