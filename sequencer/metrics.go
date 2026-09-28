package sequencer

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
)

// Rejection labels are a fixed set. Raw error text is not a label.
const (
	reasonInvalidSignature   = "invalid_signature"
	reasonWrongOwner         = "wrong_owner"
	reasonWrongChain         = "wrong_chain"
	reasonWrongInstance      = "wrong_instance"
	reasonUnsupportedCommand = "unsupported_command"
	reasonStaleNonce         = "stale_nonce"
	reasonDuplicate          = "duplicate"
	reasonMalformed          = "malformed"
	reasonTooLarge           = "too_large"
	reasonInternal           = "internal"
)

type metrics struct {
	reg              *prometheus.Registry
	received         *prometheus.CounterVec
	accepted         *prometheus.CounterVec
	rejected         *prometheus.CounterVec
	pending          prometheus.Gauge
	inflight         prometheus.Gauge
	finalizedCmds    prometheus.Counter
	batchesSubmitted prometheus.Counter
	batchesFinalized prometheus.Counter
	batchesFailed    prometheus.Counter
	batchSize        prometheus.Histogram
	submitSeconds    prometheus.Histogram
	admitSeconds     prometheus.Histogram
	journalSeconds   prometheus.Histogram
	chainSeconds     prometheus.Histogram
	lastFinalized    prometheus.Gauge
	nextPosition     prometheus.Gauge
	backoff          prometheus.Gauge
	chainUp          prometheus.Gauge
}

func newMetrics() *metrics {
	reg := prometheus.NewRegistry()
	m := &metrics{
		reg: reg,
		received: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sequencer_commands_received_total",
			Help: "Commands presented for admission.",
		}, []string{"command_type"}),
		accepted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sequencer_commands_accepted_total",
			Help: "Commands journaled as pending.",
		}, []string{"command_type"}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sequencer_commands_rejected_total",
			Help: "Commands refused before a new pending position was kept.",
		}, []string{"reason"}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sequencer_pending_commands",
			Help: "Admitted commands waiting to be submitted.",
		}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sequencer_inflight_commands",
			Help: "Commands in the batch currently being submitted.",
		}),
		finalizedCmds: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sequencer_finalized_commands_total",
			Help: "Commands journaled as finalized after chain inclusion.",
		}),
		batchesSubmitted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sequencer_batches_submitted_total",
			Help: "Batch transactions broadcast to the chain.",
		}),
		batchesFinalized: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sequencer_batches_finalized_total",
			Help: "Batch transactions included successfully.",
		}),
		batchesFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sequencer_batches_failed_total",
			Help: "Batch attempts that did not finalize.",
		}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sequencer_batch_size",
			Help:    "Commands in a broadcast batch.",
			Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128},
		}),
		submitSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sequencer_batch_submission_duration_seconds",
			Help:    "Wall time of the chain broadcast and inclusion wait.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}),
		admitSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sequencer_admission_duration_seconds",
			Help:    "Wall time to validate, journal, and accept or reject one command.",
			Buckets: prometheus.DefBuckets,
		}),
		journalSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sequencer_journal_append_duration_seconds",
			Help:    "Wall time to append and fsync journal records.",
			Buckets: prometheus.DefBuckets,
		}),
		chainSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sequencer_chain_query_duration_seconds",
			Help:    "Wall time of a chain head or nonce query.",
			Buckets: prometheus.DefBuckets,
		}),
		lastFinalized: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sequencer_last_finalized_batch",
			Help: "Latest finalized batch number observed from the chain or from a local inclusion.",
		}),
		nextPosition: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sequencer_next_position",
			Help: "Next sequencer position that will be assigned. Zero means the sequence is exhausted.",
		}),
		backoff: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sequencer_retry_backoff_seconds",
			Help: "Current delay applied after a failed batch attempt. Zero after a successful inclusion.",
		}),
		chainUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sequencer_chain_connected",
			Help: "1 when the latest chain call succeeded, otherwise 0.",
		}),
	}
	reg.MustRegister(
		m.received, m.accepted, m.rejected,
		m.pending, m.inflight, m.finalizedCmds,
		m.batchesSubmitted, m.batchesFinalized, m.batchesFailed,
		m.batchSize, m.submitSeconds, m.admitSeconds, m.journalSeconds, m.chainSeconds,
		m.lastFinalized, m.nextPosition, m.backoff, m.chainUp,
	)
	return m
}

func commandTypeLabel(cmd canonical.Command) string {
	switch cmd.Type {
	case canonical.CommandTypePlace:
		return "place"
	case canonical.CommandTypeCancel:
		return "cancel"
	default:
		return "unknown"
	}
}

func reasonLabel(err error) string {
	switch {
	case errors.Is(err, ErrSignature), errors.Is(err, ErrPubKey):
		return reasonInvalidSignature
	case errors.Is(err, ErrOwner), errors.Is(err, ErrPubKeyMismatch):
		return reasonWrongOwner
	case errors.Is(err, ErrChainID):
		return reasonWrongChain
	case errors.Is(err, ErrInstance):
		return reasonWrongInstance
	case errors.Is(err, ErrUnsupportedCommand), errors.Is(err, ErrUnsupportedVersion):
		return reasonUnsupportedCommand
	case errors.Is(err, ErrStaleNonce), errors.Is(err, ErrNonce):
		return reasonStaleNonce
	case errors.Is(err, ErrDuplicate):
		return reasonDuplicate
	case errors.Is(err, ErrMalformed):
		return reasonMalformed
	case errors.Is(err, ErrTooLarge):
		return reasonTooLarge
	default:
		return reasonInternal
	}
}

func (m *metrics) observeAdmission(start time.Time, kind string, err error) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.admitSeconds.Observe(time.Since(start).Seconds())
	if err != nil {
		m.rejected.WithLabelValues(reasonLabel(err)).Inc()
		return
	}
	m.accepted.WithLabelValues(kind).Inc()
}

func (m *metrics) countReceived(kind string) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.received.WithLabelValues(kind).Inc()
}

func (m *metrics) countRejected(reason string) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.rejected.WithLabelValues(reason).Inc()
}

func (m *metrics) observeJournal(start time.Time) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.journalSeconds.Observe(time.Since(start).Seconds())
}

func (m *metrics) observeChain(start time.Time) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.chainSeconds.Observe(time.Since(start).Seconds())
}

func (m *metrics) setChainUp(ok bool) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	if ok {
		m.chainUp.Set(1)
		return
	}
	m.chainUp.Set(0)
}

func (m *metrics) setLastBatch(n uint64) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.lastFinalized.Set(float64(n))
}

func (m *metrics) setGauges(pending, inflight int, next uint64) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.pending.Set(float64(pending))
	m.inflight.Set(float64(inflight))
	m.nextPosition.Set(float64(next))
}

func (m *metrics) setBackoff(d time.Duration) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.backoff.Set(d.Seconds())
}

func (m *metrics) observeSubmit(start time.Time, size int) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.batchesSubmitted.Inc()
	m.batchSize.Observe(float64(size))
	m.submitSeconds.Observe(time.Since(start).Seconds())
}

func (m *metrics) countFinalized(commands int, batch uint64) {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.batchesFinalized.Inc()
	m.finalizedCmds.Add(float64(commands))
	m.lastFinalized.Set(float64(batch))
}

func (m *metrics) countBatchFailed() {
	if m == nil {
		return
	}
	defer func() { _ = recover() }()
	m.batchesFailed.Inc()
}
