package keeper

import (
	"context"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 100
)

func pageLimit(limit uint32) (int, error) {
	switch {
	case limit == 0:
		return defaultPageLimit, nil
	case limit > maxPageLimit:
		return 0, types.ErrLimit
	default:
		return int(limit), nil
	}
}

func (k Keeper) iteratePrefix(ctx context.Context, prefix []byte, fn func(key, value []byte) (bool, error)) error {
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	iter, err := kv.Iterator(prefix, prefixEnd(prefix))
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key := append([]byte(nil), iter.Key()...)
		value := append([]byte(nil), iter.Value()...)
		stop, err := fn(key, value)
		if err != nil || stop {
			return err
		}
	}
	return nil
}

func (k Keeper) listMarkets(ctx context.Context, offset uint64, limit int) ([]types.Market, uint64, error) {
	var out []types.Market
	var seen uint64
	var more bool
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixMarket}, func(key, value []byte) (bool, error) {
		if _, err := canonical.DecodeMarketKey(key); err != nil {
			return false, err
		}
		if seen < offset {
			seen++
			return false, nil
		}
		if len(out) == limit {
			more = true
			return true, nil
		}
		market, err := types.DecodeMarket(value)
		if err != nil {
			return false, err
		}
		out = append(out, market)
		seen++
		return false, nil
	})
	if err != nil {
		return nil, 0, err
	}
	if !more {
		return out, 0, nil
	}
	return out, offset + uint64(len(out)), nil
}

func (k Keeper) listBalances(ctx context.Context, owner []byte, offset uint64, limit int) ([]types.Balance, []domain.AssetID, uint64, error) {
	prefix, err := canonical.BalanceOwnerPrefix(owner)
	if err != nil {
		return nil, nil, 0, err
	}
	var bals []types.Balance
	var assets []domain.AssetID
	var seen uint64
	var more bool
	err = k.iteratePrefix(ctx, prefix, func(key, value []byte) (bool, error) {
		_, assetID, err := canonical.DecodeBalanceKey(key)
		if err != nil {
			return false, err
		}
		if seen < offset {
			seen++
			return false, nil
		}
		if len(bals) == limit {
			more = true
			return true, nil
		}
		bal, err := types.DecodeBalance(value)
		if err != nil {
			return false, err
		}
		bals = append(bals, bal)
		assets = append(assets, assetID)
		seen++
		return false, nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	if !more {
		return bals, assets, 0, nil
	}
	return bals, assets, offset + uint64(len(bals)), nil
}

func (k Keeper) listOpenOrders(ctx context.Context, owner []byte, marketID domain.MarketID, offset uint64, limit int) ([]types.StoredOrder, uint64, error) {
	prefix, err := canonical.OwnerOpenOrderPrefix(owner)
	if err != nil {
		return nil, 0, err
	}
	var out []types.StoredOrder
	var seen uint64
	var more bool
	err = k.iteratePrefix(ctx, prefix, func(key, _ []byte) (bool, error) {
		_, gotMarket, id, err := canonical.DecodeOwnerOpenOrderKey(key)
		if err != nil {
			return false, err
		}
		if marketID != 0 && gotMarket != marketID {
			return false, nil
		}
		if seen < offset {
			seen++
			return false, nil
		}
		if len(out) == limit {
			more = true
			return true, nil
		}
		order, err := k.GetOrder(ctx, id)
		if err != nil {
			return false, err
		}
		out = append(out, order)
		seen++
		return false, nil
	})
	if err != nil {
		return nil, 0, err
	}
	if !more {
		return out, 0, nil
	}
	return out, offset + uint64(len(out)), nil
}

func (k Keeper) listBook(ctx context.Context, marketID domain.MarketID, side domain.Side, offset, limit int) ([]types.StoredOrder, error) {
	var prefix []byte
	var err error
	switch side {
	case domain.SideSell:
		prefix, err = canonical.AskMarketPrefix(marketID)
	case domain.SideBuy:
		prefix, err = canonical.BidMarketPrefix(marketID)
	default:
		return nil, domain.ErrInvalidSide
	}
	if err != nil {
		return nil, err
	}
	var out []types.StoredOrder
	skipped := 0
	err = k.iteratePrefix(ctx, prefix, func(key, value []byte) (bool, error) {
		if len(value) != len(domain.OrderID{}) {
			return false, types.ErrCorrupt
		}
		if skipped < offset {
			skipped++
			return false, nil
		}
		if len(out) == limit {
			return true, nil
		}
		var id domain.OrderID
		copy(id[:], value)
		order, err := k.GetOrder(ctx, id)
		if err != nil {
			return false, err
		}
		out = append(out, order)
		return false, nil
	})
	return out, err
}

func (k Keeper) listTrades(ctx context.Context, marketID domain.MarketID, after uint64, limit int) ([]types.Trade, uint64, error) {
	prefix, err := canonical.EncodeMarketKey(marketID)
	if err != nil {
		return nil, 0, err
	}
	prefix[0] = canonical.PrefixTrade
	start := prefix
	if after > 0 {
		next, err := domain.NextSequence(domain.Sequence(after))
		if err != nil {
			return nil, 0, err
		}
		start, err = canonical.EncodeTradeKey(marketID, uint64(next))
		if err != nil {
			return nil, 0, err
		}
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return nil, 0, err
	}
	iter, err := kv.Iterator(start, prefixEnd(prefix))
	if err != nil {
		return nil, 0, err
	}
	defer iter.Close()
	var out []types.Trade
	for ; iter.Valid() && len(out) < limit; iter.Next() {
		trade, err := types.DecodeTrade(append([]byte(nil), iter.Value()...))
		if err != nil {
			return nil, 0, err
		}
		if trade.MarketID != marketID {
			return nil, 0, types.ErrCorrupt
		}
		out = append(out, trade)
	}
	if !iter.Valid() || len(out) == 0 {
		return out, 0, nil
	}
	return out, out[len(out)-1].Sequence, nil
}
