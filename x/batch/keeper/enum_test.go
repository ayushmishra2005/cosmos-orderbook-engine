package keeper

import (
	"math"
	"testing"

	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

func TestBatchEnumsDoNotNarrow(t *testing.T) {
	for _, v := range []int32{257, -1, math.MaxInt32} {
		if _, err := asSide(v1.Side(v)); err == nil {
			t.Fatalf("side %d", v)
		}
		if _, err := asOrderType(v1.OrderType(v)); err == nil {
			t.Fatalf("type %d", v)
		}
		if _, err := asTimeInForce(v1.TimeInForce(v)); err == nil {
			t.Fatalf("tif %d", v)
		}
	}
}
