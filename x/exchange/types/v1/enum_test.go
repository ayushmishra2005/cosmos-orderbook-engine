package v1

import (
	"math"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestGenesisEnumsDoNotNarrow(t *testing.T) {
	owner := sdk.AccAddress(bytes20(3)).String()
	for _, v := range []int32{257, -1, math.MaxInt32} {
		_, err := decodeGenesisOrder(&GenesisOrder{
			OrderId: bytes32(1), Owner: owner, MarketId: 1,
			Side: Side(v), OrderType: OrderType_ORDER_TYPE_LIMIT,
			TimeInForce: TimeInForce_TIME_IN_FORCE_GTC,
			PriceTicks:  1, OriginalLots: 1, RemainingLots: 1, Sequence: 1, CommandNonce: 1,
		})
		if err == nil {
			t.Fatalf("side %d was accepted", v)
		}
		_, err = decodeGenesisOrder(&GenesisOrder{
			OrderId: bytes32(1), Owner: owner, MarketId: 1,
			Side: Side_SIDE_BUY, OrderType: OrderType(v),
			TimeInForce: TimeInForce_TIME_IN_FORCE_GTC,
			PriceTicks:  1, OriginalLots: 1, RemainingLots: 1, Sequence: 1, CommandNonce: 1,
		})
		if err == nil {
			t.Fatalf("type %d was accepted", v)
		}
		_, err = decodeGenesisOrder(&GenesisOrder{
			OrderId: bytes32(1), Owner: owner, MarketId: 1,
			Side: Side_SIDE_SELL, OrderType: OrderType_ORDER_TYPE_LIMIT,
			TimeInForce: TimeInForce(v),
			PriceTicks:  1, OriginalLots: 1, RemainingLots: 1, Sequence: 1, CommandNonce: 1,
		})
		if err == nil {
			t.Fatalf("tif %d was accepted", v)
		}
	}
	got, err := domainSide(Side_SIDE_BUY)
	if err != nil || got != domain.SideBuy {
		t.Fatal(got, err)
	}
}

func bytes20(b byte) []byte {
	out := make([]byte, 20)
	for i := range out {
		out[i] = b
	}
	return out
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	out[0] = b
	return out
}
