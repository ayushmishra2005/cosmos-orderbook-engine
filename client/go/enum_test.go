package orderbook

import (
	"math"
	"testing"

	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestQueryEnumsDoNotNarrow(t *testing.T) {
	for _, v := range []int32{257, -1, math.MaxInt32} {
		if _, err := domainSide(exchangev1.Side(v)); err == nil {
			t.Fatalf("side %d", v)
		}
		if _, err := domainOrderType(exchangev1.OrderType(v)); err == nil {
			t.Fatalf("type %d", v)
		}
		if _, err := domainTimeInForce(exchangev1.TimeInForce(v)); err == nil {
			t.Fatalf("tif %d", v)
		}
	}
}
