package keeper

import (
	"math"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestProtoEnumsDoNotNarrow(t *testing.T) {
	for _, v := range []int32{257, -1, math.MaxInt32} {
		if side, err := asSide(v1.Side(v)); err == nil || side.Valid() {
			t.Fatalf("side %d -> %d %v", v, side, err)
		}
		if typ, err := asOrderType(v1.OrderType(v)); err == nil || typ.Valid() {
			t.Fatalf("type %d -> %d %v", v, typ, err)
		}
		if tif, err := asTimeInForce(v1.TimeInForce(v)); err == nil || tif.Valid() {
			t.Fatalf("tif %d -> %d %v", v, tif, err)
		}
	}
	side, err := asSide(v1.Side_SIDE_BUY)
	if err != nil || side != domain.SideBuy {
		t.Fatal(side, err)
	}
}
