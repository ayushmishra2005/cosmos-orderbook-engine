// Package sequencer is an off-chain batch submitter.
// It checks owner signatures, assigns a local admission order, and stores
// that order in a journal. Validators still execute commands and settle trades.
// An admission response is provisional until the chain finalizes the batch.
// Validators remain authoritative for fills and settlement.
package sequencer
