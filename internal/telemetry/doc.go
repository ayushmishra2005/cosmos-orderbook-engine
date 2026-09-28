// Package telemetry records Prometheus metrics for the sequencer process and
// for exchange and batch execution.
//
// Recording is best-effort. A panic inside a recorder is swallowed. Callers
// must not branch on metric success, and must not write metric values, wall
// clocks, or sample decisions into consensus state.
package telemetry
