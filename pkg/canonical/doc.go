// Package canonical implements deterministic byte encodings for order IDs,
// exchange state keys, signed batch commands, command results, and batch
// commitments. Hashing and key ordering must not use protobuf, JSON, or Go
// map iteration.
package canonical
