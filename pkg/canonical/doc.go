// Package canonical implements deterministic byte encodings for order IDs,
// exchange state keys, and signed batch commands. Hashing and key ordering
// must not use protobuf, JSON, or Go map iteration.
package canonical
