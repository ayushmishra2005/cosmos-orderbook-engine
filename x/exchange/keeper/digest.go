package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// snapshotDomain separates a test digest from order IDs, batch IDs, and commitments.
// The digest is not a consensus commitment.
const snapshotDomain = "cosmos-orderbook/test-snapshot/v1"

// StateDigestForTest hashes exchange KV pairs in key order.
// It is for tests. It is not a state root and it is not committed.
func (k Keeper) StateDigestForTest(ctx context.Context) ([32]byte, error) {
	return k.hashStore(ctx, snapshotDomain)
}

// SnapshotDigest is StateDigestForTest.
// The name is for tests that compare snapshots. It is not a consensus commitment.
func (k Keeper) SnapshotDigest(ctx context.Context) ([32]byte, error) {
	return k.StateDigestForTest(ctx)
}

func (k Keeper) hashStore(ctx context.Context, domain string) ([32]byte, error) {
	h := sha256.New()
	writePrefixed(h, []byte(domain))
	for prefix := byte(0x01); prefix <= 0x0F; prefix++ {
		err := k.iteratePrefix(ctx, []byte{prefix}, func(key, value []byte) (bool, error) {
			writePrefixed(h, key)
			writePrefixed(h, value)
			return false, nil
		})
		if err != nil {
			return [32]byte{}, err
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func writePrefixed(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}
