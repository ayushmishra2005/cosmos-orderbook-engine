package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/core/store"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// ExpireOrders removes up to max orders whose expiry height is at or before
// height. Iteration follows the expiration index and stops at the first later
// height. It does not walk the order book. No nonce changes.
func (k Keeper) ExpireOrders(ctx context.Context, height uint64, max int) (int, error) {
	if max <= 0 {
		return 0, types.ErrBound
	}
	var n int
	err := k.commit(ctx, func(ctx sdk.Context) error {
		var err error
		n, err = k.expire(ctx, height, max)
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (k Keeper) expire(ctx sdk.Context, height uint64, max int) (int, error) {
	kv, err := k.kv(ctx)
	if err != nil {
		return 0, err
	}
	prefix := []byte{canonical.PrefixExpiration}
	iter, err := kv.Iterator(prefix, prefixEnd(prefix))
	if err != nil {
		return 0, err
	}
	due, err := collectExpired(k, ctx, iter, height, max)
	if cerr := iter.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if len(due) == 0 {
		return 0, nil
	}

	bals := &balSet{k: k, ctx: ctx}
	type release struct {
		order  types.StoredOrder
		asset  domain.AssetID
		amount uint64
		index  int
	}
	releases := make([]release, 0, len(due))
	for _, order := range due {
		if order.Order.TimeInForce != domain.TimeInForceGTD || order.Order.ExpiryHeight == 0 || order.Order.ExpiryHeight > height {
			return 0, types.ErrCorrupt
		}
		market, err := k.GetMarket(ctx, order.Order.MarketID)
		if err != nil {
			return 0, err
		}
		asset, amount, err := reserveOf(market, order.Order)
		if err != nil {
			return 0, err
		}
		idx, err := bals.touch(order.Order.Owner, asset)
		if err != nil {
			return 0, err
		}
		if err := bals.sub(idx, true, amount, types.ErrSettlement); err != nil {
			return 0, err
		}
		if err := bals.add(idx, false, amount); err != nil {
			return 0, err
		}
		releases = append(releases, release{order: order, asset: asset, amount: amount, index: idx})
	}
	for _, rel := range releases {
		if err := k.removeResting(ctx, rel.order); err != nil {
			return 0, err
		}
	}
	for _, bal := range bals.items {
		if err := k.setBalance(ctx, bal.owner, bal.asset, bal.bal); err != nil {
			return 0, err
		}
	}
	if err := k.bumpRevision(ctx); err != nil {
		return 0, err
	}
	return len(releases), nil
}

func collectExpired(k Keeper, ctx sdk.Context, iter store.Iterator, height uint64, max int) ([]types.StoredOrder, error) {
	due := make([]types.StoredOrder, 0, max)
	for iter.Valid() && len(due) < max {
		key := append([]byte(nil), iter.Key()...)
		expHeight, id, err := canonical.DecodeExpirationKey(key)
		if err != nil {
			return nil, err
		}
		if expHeight > height {
			return due, nil
		}
		order, err := k.GetOrder(ctx, id)
		if err != nil {
			if errors.Is(err, types.ErrNotFound) {
				return nil, types.ErrCorrupt
			}
			return nil, err
		}
		if order.Order.ID != id || order.Order.ExpiryHeight != expHeight {
			return nil, types.ErrCorrupt
		}
		due = append(due, order)
		iter.Next()
	}
	return due, nil
}
