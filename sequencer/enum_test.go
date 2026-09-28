package sequencer

import (
	"math"
	"testing"

	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

func TestSequencerEnumsDoNotNarrow(t *testing.T) {
	for _, v := range []int32{257, -1, math.MaxInt32} {
		if _, err := sideFromProto(batchv1.Side(v)); err == nil {
			t.Fatalf("side %d", v)
		}
		if _, err := orderTypeFromProto(batchv1.OrderType(v)); err == nil {
			t.Fatalf("type %d", v)
		}
		if _, err := tifFromProto(batchv1.TimeInForce(v)); err == nil {
			t.Fatalf("tif %d", v)
		}
	}
}
