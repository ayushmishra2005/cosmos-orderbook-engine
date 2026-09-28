package telemetry

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordPlaceAndBatch(t *testing.T) {
	beforeOrders := testutil.ToFloat64(ordersProcessed)
	beforeFills := testutil.ToFloat64(fillsTotal)
	beforeTrades := testutil.ToFloat64(tradesExecuted)
	RecordPlace(3, time.Millisecond, 2*time.Millisecond)
	if got := testutil.ToFloat64(ordersProcessed); got != beforeOrders+1 {
		t.Fatalf("orders %v", got)
	}
	if got := testutil.ToFloat64(fillsTotal); got != beforeFills+3 {
		t.Fatalf("fills %v", got)
	}
	if got := testutil.ToFloat64(tradesExecuted); got != beforeTrades+3 {
		t.Fatalf("trades %v", got)
	}

	beforeCancel := testutil.ToFloat64(ordersCancelled)
	RecordCancel()
	if got := testutil.ToFloat64(ordersCancelled); got != beforeCancel+1 {
		t.Fatalf("cancels %v", got)
	}

	beforeExp := testutil.ToFloat64(ordersExpired)
	RecordExpired(0)
	RecordExpired(4)
	if got := testutil.ToFloat64(ordersExpired); got != beforeExp+4 {
		t.Fatalf("expired %v", got)
	}

	beforeBatch := testutil.ToFloat64(batchCommands)
	RecordBatch(12, 5*time.Millisecond)
	if got := testutil.ToFloat64(batchCommands); got != beforeBatch+12 {
		t.Fatalf("batch commands %v", got)
	}
}
