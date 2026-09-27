package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/core/store"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// bookSource is the Cosmos-backed matcher cursor for one side of one market.
// It yields orders in canonical key order and copies iterator buffers.
type bookSource struct {
	k        Keeper
	ctx      context.Context
	kv       store.KVStore
	marketID domain.MarketID
	side     domain.Side
	iter     store.Iterator
	peeked   *domain.Order
	closed   bool
}

func (k Keeper) openBook(ctx context.Context, marketID domain.MarketID, makerSide domain.Side) (*bookSource, error) {
	var prefix []byte
	var err error
	switch makerSide {
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
	kv, err := k.kv(ctx)
	if err != nil {
		return nil, err
	}
	iter, err := kv.Iterator(prefix, prefixEnd(prefix))
	if err != nil {
		return nil, err
	}
	return &bookSource{
		k:        k,
		ctx:      ctx,
		kv:       kv,
		marketID: marketID,
		side:     makerSide,
		iter:     iter,
	}, nil
}

func (s *bookSource) Peek() (domain.Order, bool, error) {
	if s.closed {
		return domain.Order{}, false, types.ErrCorrupt
	}
	if s.peeked != nil {
		return *s.peeked, true, nil
	}
	if !s.iter.Valid() {
		// cacheMergeIterator.Error is set whenever Valid is false. That is
		// exhaustion, not a failed read. Corrupt keys are reported below.
		return domain.Order{}, false, nil
	}
	key := append([]byte(nil), s.iter.Key()...)
	val := append([]byte(nil), s.iter.Value()...)
	if len(val) != len(domain.OrderID{}) {
		return domain.Order{}, false, types.ErrCorrupt
	}
	var id domain.OrderID
	copy(id[:], val)

	var price domain.Price
	var sequence domain.Sequence
	var marketID domain.MarketID
	var err error
	switch s.side {
	case domain.SideSell:
		marketID, price, sequence, err = canonical.DecodeAskKey(key)
	case domain.SideBuy:
		marketID, price, sequence, err = canonical.DecodeBidKey(key)
	default:
		err = domain.ErrInvalidSide
	}
	if err != nil {
		return domain.Order{}, false, err
	}
	order, err := s.k.GetOrder(s.ctx, id)
	if err != nil {
		if errors.Is(err, types.ErrNotFound) {
			return domain.Order{}, false, types.ErrCorrupt
		}
		return domain.Order{}, false, err
	}
	got := order.Order
	if got.MarketID != s.marketID || marketID != s.marketID || got.Side != s.side || got.Price != price || got.Sequence != sequence || got.ID != id {
		return domain.Order{}, false, types.ErrCorrupt
	}
	s.peeked = &got
	return got, true, nil
}

func (s *bookSource) Next() error {
	if s.closed {
		return types.ErrCorrupt
	}
	s.peeked = nil
	if !s.iter.Valid() {
		return types.ErrCorrupt
	}
	s.iter.Next()
	return nil
}

func (s *bookSource) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.peeked = nil
	return s.iter.Close()
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

func bookKey(order domain.Order) ([]byte, error) {
	switch order.Side {
	case domain.SideBuy:
		return canonical.EncodeBidKey(order.MarketID, order.Price, order.Sequence)
	case domain.SideSell:
		return canonical.EncodeAskKey(order.MarketID, order.Price, order.Sequence)
	default:
		return nil, domain.ErrInvalidSide
	}
}

func (k Keeper) putActive(ctx context.Context, order types.StoredOrder) error {
	key, err := canonical.EncodeActiveOrderKey(order.Order.ID)
	if err != nil {
		return err
	}
	bz, err := types.EncodeOrder(order)
	if err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	return kv.Set(key, bz)
}

func (k Keeper) putResting(ctx context.Context, order types.StoredOrder) error {
	if err := k.putActive(ctx, order); err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	idVal := append([]byte(nil), order.Order.ID[:]...)
	book, err := bookKey(order.Order)
	if err != nil {
		return err
	}
	if err := kv.Set(book, idVal); err != nil {
		return err
	}
	ownerKey, err := canonical.EncodeOwnerOpenOrderKey(order.Order.Owner, order.Order.MarketID, order.Order.ID)
	if err != nil {
		return err
	}
	if err := kv.Set(ownerKey, append([]byte(nil), idVal...)); err != nil {
		return err
	}
	if order.Order.TimeInForce != domain.TimeInForceGTD {
		return nil
	}
	expKey, err := canonical.EncodeExpirationKey(order.Order.ExpiryHeight, order.Order.ID)
	if err != nil {
		return err
	}
	return kv.Set(expKey, append([]byte(nil), idVal...))
}

func (k Keeper) removeResting(ctx context.Context, order types.StoredOrder) error {
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	active, err := canonical.EncodeActiveOrderKey(order.Order.ID)
	if err != nil {
		return err
	}
	if err := kv.Delete(active); err != nil {
		return err
	}
	book, err := bookKey(order.Order)
	if err != nil {
		return err
	}
	if err := kv.Delete(book); err != nil {
		return err
	}
	ownerKey, err := canonical.EncodeOwnerOpenOrderKey(order.Order.Owner, order.Order.MarketID, order.Order.ID)
	if err != nil {
		return err
	}
	if err := kv.Delete(ownerKey); err != nil {
		return err
	}
	if order.Order.TimeInForce == domain.TimeInForceGTD {
		expKey, err := canonical.EncodeExpirationKey(order.Order.ExpiryHeight, order.Order.ID)
		if err != nil {
			return err
		}
		if err := kv.Delete(expKey); err != nil {
			return err
		}
	}
	return k.deleteClientOrder(ctx, order.Order.Owner, order.Order.ID)
}

func (k Keeper) deleteClientOrder(ctx context.Context, owner []byte, id domain.OrderID) error {
	prefix, err := canonical.ActiveClientOrderPrefix(owner)
	if err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	iter, err := kv.Iterator(prefix, prefixEnd(prefix))
	if err != nil {
		return err
	}
	var drop [][]byte
	for ; iter.Valid(); iter.Next() {
		if bytes.Equal(iter.Value(), id[:]) {
			drop = append(drop, append([]byte(nil), iter.Key()...))
		}
	}
	if err := iter.Close(); err != nil {
		return err
	}
	for _, key := range drop {
		if err := kv.Delete(key); err != nil {
			return err
		}
	}
	return nil
}
