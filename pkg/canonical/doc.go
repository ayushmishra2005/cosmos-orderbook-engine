// Package canonical implements deterministic byte encodings for order IDs
// and exchange state keys. Hashing and key ordering must not use protobuf,
// JSON, or Go map iteration.
package canonical
