package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"

	"cosmossdk.io/core/store"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

const snapshotDomain = "cosmos-orderbook/test-batch-snapshot/v1"

// SnapshotDigest is the test digest of the batch store.
// It is not a consensus commitment.
func (k Keeper) SnapshotDigest(ctx context.Context) ([32]byte, error) {
	h := sha256.New()
	writePrefixed(h, []byte(snapshotDomain))
	for _, prefix := range []byte{types.PrefixParams, types.PrefixBatch, types.PrefixBatchID} {
		if err := k.iteratePrefix(ctx, []byte{prefix}, func(key, value []byte) error {
			writePrefixed(h, key)
			writePrefixed(h, value)
			return nil
		}); err != nil {
			return [32]byte{}, err
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func (k Keeper) iteratePrefix(ctx context.Context, prefix []byte, fn func(key, value []byte) error) error {
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	end := prefixEnd(prefix)
	var iter store.Iterator
	if end == nil {
		iter, err = kv.Iterator(prefix, nil)
	} else {
		iter, err = kv.Iterator(prefix, end)
	}
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key := append([]byte(nil), iter.Key()...)
		value := append([]byte(nil), iter.Value()...)
		if err := fn(key, value); err != nil {
			return err
		}
	}
	return nil
}

func prefixEnd(prefix []byte) []byte {
	end := make([]byte, len(prefix))
	copy(end, prefix)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end
		}
	}
	return nil
}

func writePrefixed(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}
