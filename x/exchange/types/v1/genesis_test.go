package v1

import (
	"bytes"
	"errors"
	"math"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestDefaultGenesisValidates(t *testing.T) {
	if err := DefaultGenesis().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGenesisRejectsCorruptOrders(t *testing.T) {
	owner := sampleOwner()
	cases := []struct {
		name   string
		edit   func(*GenesisState)
		target error
	}{
		{name: "unknown market", edit: func(gs *GenesisState) { gs.Orders[0].MarketId = 9 }, target: domain.ErrInvalidMarket},
		{name: "duplicate order id", edit: func(gs *GenesisState) {
			dup := *gs.Orders[0]
			gs.Orders = append(gs.Orders, &dup)
		}, target: exchangetypes.ErrExists},
		{name: "duplicate client id", edit: func(gs *GenesisState) {
			gs.Orders[0].ClientOrderId = []byte("desk")
			second := *gs.Orders[0]
			var id [32]byte
			id[0] = 2
			second.OrderId = id[:]
			second.Sequence = 2
			gs.Orders = append(gs.Orders, &second)
			gs.OrderSequences[0].Sequence = 2
			gs.Balances[0].Locked = 8
		}, target: exchangetypes.ErrExists},
		{name: "duplicate sequence", edit: func(gs *GenesisState) {
			second := *gs.Orders[0]
			var id [32]byte
			id[0] = 2
			second.OrderId = id[:]
			gs.Orders = append(gs.Orders, &second)
			gs.Balances[0].Locked = 8
		}, target: exchangetypes.ErrExists},
		{name: "market order resting", edit: func(gs *GenesisState) {
			gs.Orders[0].OrderType = OrderType_ORDER_TYPE_MARKET
		}, target: domain.ErrInvalidOrderType},
		{name: "ioc resting", edit: func(gs *GenesisState) {
			gs.Orders[0].TimeInForce = TimeInForce_TIME_IN_FORCE_IOC
		}, target: domain.ErrInvalidTimeInForce},
		{name: "remaining above original", edit: func(gs *GenesisState) {
			gs.Orders[0].RemainingLots = 5
		}, target: domain.ErrInvalidQuantity},
		{name: "zero sequence", edit: func(gs *GenesisState) { gs.Orders[0].Sequence = 0 }, target: domain.ErrInvalidSequence},
		{name: "bad expiry", edit: func(gs *GenesisState) {
			gs.Orders[0].TimeInForce = TimeInForce_TIME_IN_FORCE_GTD
			gs.Orders[0].ExpiryHeight = 0
		}, target: domain.ErrInvalidExpiry},
		{name: "sequence behind", edit: func(gs *GenesisState) {
			gs.Orders[0].Sequence = 2
			gs.OrderSequences[0].Sequence = 1
		}, target: exchangetypes.ErrCorrupt},
		{name: "nonce behind", edit: func(gs *GenesisState) {
			gs.Orders[0].CommandNonce = 2
			gs.Nonces[0].Nonce = 1
		}, target: exchangetypes.ErrCorrupt},
		{name: "locked without order", edit: func(gs *GenesisState) {
			gs.Orders = nil
			gs.Balances[0].Locked = 4
		}, target: exchangetypes.ErrCorrupt},
		{name: "order without lock", edit: func(gs *GenesisState) {
			gs.Balances[0].Locked = 0
			gs.Balances[0].Available = 4
		}, target: exchangetypes.ErrInsufficientBalance},
		{name: "fee gross", edit: func(gs *GenesisState) { gs.Orders[0].TakerGross = 1 }, target: exchangetypes.ErrCorrupt},
		{name: "duplicate balance", edit: func(gs *GenesisState) {
			gs.Balances = append(gs.Balances, gs.Balances[0])
		}, target: exchangetypes.ErrExists},
		{name: "empty balance", edit: func(gs *GenesisState) {
			gs.Orders = nil
			gs.Balances[0].Locked = 0
			gs.Balances[0].Available = 0
		}, target: exchangetypes.ErrInvalidAmount},
		{name: "balance capacity", edit: func(gs *GenesisState) {
			gs.Orders = nil
			gs.Nonces = nil
			gs.OrderSequences = nil
			gs.Balances[0].Available = math.MaxUint64
			gs.Balances[0].Locked = 1
		}, target: arithmetic.ErrOverflow},
		{name: "reserve overflow", edit: func(gs *GenesisState) {
			gs.Orders[0].Side = Side_SIDE_BUY
			gs.Orders[0].PriceTicks = math.MaxUint64
			gs.Orders[0].OriginalLots = 2
			gs.Orders[0].RemainingLots = 2
			gs.Balances[0].AssetId = 2
		}, target: arithmetic.ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := restingSellGenesis(owner)
			tc.edit(gs)
			err := gs.Validate()
			if !errors.Is(err, tc.target) {
				t.Fatalf("got %v, want %v", err, tc.target)
			}
		})
	}
}

func restingSellGenesis(owner string) *GenesisState {
	gs := DefaultGenesis()
	var id [32]byte
	id[0] = 1
	gs.Orders = []*GenesisOrder{{
		OrderId:       id[:],
		Owner:         owner,
		MarketId:      1,
		Side:          Side_SIDE_SELL,
		OrderType:     OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   TimeInForce_TIME_IN_FORCE_GTC,
		PriceTicks:    3,
		OriginalLots:  4,
		RemainingLots: 4,
		Sequence:      1,
		CommandNonce:  1,
	}}
	gs.Balances = []*GenesisBalance{{Owner: owner, AssetId: 1, Locked: 4}}
	gs.Nonces = []*AccountNonce{{Owner: owner, Nonce: 1}}
	gs.OrderSequences = []*MarketSequence{{MarketId: 1, Sequence: 1}}
	return gs
}

func sampleOwner() string {
	return sdk.AccAddress(bytes.Repeat([]byte{0x22}, 20)).String()
}
