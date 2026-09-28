package telemetry

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	ordersProcessed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "exchange_orders_processed_total",
		Help: "Place commands that committed.",
	})
	tradesExecuted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "exchange_trades_executed_total",
		Help: "Trades written by committed place commands.",
	})
	ordersCancelled = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "exchange_orders_cancelled_total",
		Help: "Cancel commands that committed.",
	})
	ordersExpired = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "exchange_orders_expired_total",
		Help: "Resting orders removed by expiration.",
	})
	fillsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "exchange_fills_total",
		Help: "Fills produced by committed place commands.",
	})
	matchDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "exchange_match_duration_seconds",
		Help:    "Wall time spent in the pure matcher for a committed place.",
		Buckets: prometheus.DefBuckets,
	})
	settlementDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "exchange_settlement_duration_seconds",
		Help:    "Wall time spent building the settlement plan for a committed place.",
		Buckets: prometheus.DefBuckets,
	})
	batchCommands = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "batch_commands_executed_total",
		Help: "Commands in batches that committed.",
	})
	batchDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "batch_execution_duration_seconds",
		Help:    "Wall time of a committed FinalizeBatch call.",
		Buckets: prometheus.DefBuckets,
	})
	batchSize = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "batch_commands_per_batch",
		Help:    "Commands in a committed batch.",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128},
	})
)

func init() {
	prometheus.MustRegister(
		ordersProcessed,
		tradesExecuted,
		ordersCancelled,
		ordersExpired,
		fillsTotal,
		matchDuration,
		settlementDuration,
		batchCommands,
		batchDuration,
		batchSize,
	)
}

// RecordPlace counts one committed place. fills is the number of trades written.
// match and settle are wall durations. They are not inputs to matching or fees.
func RecordPlace(fills int, match, settle time.Duration) {
	defer func() { _ = recover() }()
	ordersProcessed.Inc()
	if fills > 0 {
		tradesExecuted.Add(float64(fills))
		fillsTotal.Add(float64(fills))
	}
	matchDuration.Observe(match.Seconds())
	settlementDuration.Observe(settle.Seconds())
}

// RecordCancel counts one committed cancel.
func RecordCancel() {
	defer func() { _ = recover() }()
	ordersCancelled.Inc()
}

// RecordExpired counts resting orders removed by a committed expiration sweep.
func RecordExpired(n int) {
	defer func() { _ = recover() }()
	if n <= 0 {
		return
	}
	ordersExpired.Add(float64(n))
}

// RecordBatch counts one committed batch. d is wall time and is not stored.
func RecordBatch(commands int, d time.Duration) {
	defer func() { _ = recover() }()
	if commands < 0 {
		commands = 0
	}
	batchCommands.Add(float64(commands))
	batchDuration.Observe(d.Seconds())
	batchSize.Observe(float64(commands))
}
