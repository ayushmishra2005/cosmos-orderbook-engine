package keeper

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
)

func corrupt(reason string) error {
	return fmt.Errorf("%w: %s", types.ErrCorrupt, reason)
}

// CheckInvariants reads the batch head and records.
// Numbers are contiguous from 1. Each previous commitment is the prior
// commitment. ResultsHash and BatchCommitment recompute from the stored record.
// It does not write and it does not repair.
//
// The record does not store the signed commands, so BatchID is checked
// against the ID index here. Tests compare BatchID to the commands they submitted.
func (k Keeper) CheckInvariants(ctx context.Context) error {
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if params.Latest == 0 {
		if !params.Head.IsZero() {
			return corrupt("head commitment without a batch")
		}
	}
	var count int
	err = k.iteratePrefix(ctx, []byte{types.PrefixBatch}, func(key, _ []byte) error {
		if len(key) != 9 {
			return corrupt("batch key")
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if uint64(count) != params.Latest {
		return corrupt("batch numbers are not contiguous")
	}
	var previous types.BatchCommitment
	for number := uint64(1); number <= params.Latest; number++ {
		batch, err := k.GetBatch(ctx, number)
		if err != nil {
			return corrupt("missing batch")
		}
		if batch.Number != number || (number > 1 && batch.Number < number) {
			return corrupt("batch number")
		}
		if batch.Previous != previous {
			return corrupt("previous commitment")
		}
		if err := k.verifyCommitment(batch); err != nil {
			return err
		}
		byID, err := k.GetBatchByID(ctx, batch.ID)
		if err != nil || byID.Number != batch.Number || byID.Commitment != batch.Commitment {
			return corrupt("batch id index")
		}
		previous = batch.Commitment
	}
	if params.Head != previous {
		return corrupt("head commitment")
	}
	var ids int
	err = k.iteratePrefix(ctx, []byte{types.PrefixBatchID}, func(key, value []byte) error {
		if len(key) != 33 {
			return corrupt("batch id key")
		}
		var id types.BatchID
		copy(id[:], key[1:])
		number, err := types.DecodeUint64(value)
		if err != nil {
			return err
		}
		if number == 0 || number > params.Latest {
			return corrupt("batch id number")
		}
		batch, err := k.GetBatch(ctx, number)
		if err != nil || batch.ID != id {
			return corrupt("batch id index")
		}
		ids++
		return nil
	})
	if err != nil {
		return err
	}
	if uint64(ids) != params.Latest {
		return corrupt("duplicate or missing batch id")
	}
	return nil
}

// BatchIDMatches reports whether id is the BatchID of these ordered commands.
func BatchIDMatches(number, revision uint64, id types.BatchID, cmds []canonical.Command) error {
	raw, err := canonical.HashBatchID(number, revision, cmds)
	if err != nil {
		return err
	}
	if types.BatchID(raw) != id {
		return fmt.Errorf("%w: batch id", types.ErrCorrupt)
	}
	return nil
}

// CommandsEqual reports whether the stored summaries stay in command order.
// It does not re-execute the commands.
func CommandsEqual(results []types.CommandResult, cmds []canonical.Command) error {
	if len(results) != len(cmds) {
		return fmt.Errorf("%w: command count", types.ErrCorrupt)
	}
	for i := range cmds {
		if results[i].Index != uint32(i) {
			return fmt.Errorf("%w: result index", types.ErrCorrupt)
		}
		if !bytes.Equal(results[i].Owner, cmds[i].Owner) {
			return fmt.Errorf("%w: result owner", types.ErrCorrupt)
		}
	}
	return nil
}
