package keeper

import (
	"context"

	"cosmossdk.io/core/store"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// Keeper owns exchange state. The matcher does not.
//
// failBeforeWrite is a test hook. When set, a command is staged in a cache
// and then aborted without Write. Production leaves it nil.
type Keeper struct {
	store           store.KVStoreService
	chainID         string
	instanceID      []byte
	bank            BankKeeper
	failBeforeWrite error
}

// NewKeeper binds the exchange store. chainID and instanceID are inputs to
// every order ID. They are not derived from a transaction hash.
func NewKeeper(svc store.KVStoreService, chainID string, instanceID []byte) (Keeper, error) {
	if svc == nil {
		return Keeper{}, types.ErrCorrupt
	}
	copied := append([]byte(nil), instanceID...)
	if _, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID:            chainID,
		ExchangeInstanceID: copied,
		Owner:              []byte{1},
		MarketID:           1,
		CommandNonce:       1,
	}); err != nil {
		return Keeper{}, err
	}
	return Keeper{store: svc, chainID: chainID, instanceID: copied}, nil
}

// WithBank attaches the bank keeper used by deposit and withdrawal.
// Trading does not call it. A nil bank rejects custody messages.
func (k Keeper) WithBank(bank BankKeeper) Keeper {
	k.bank = bank
	return k
}

// InstanceID is the exchange instance mixed into order IDs.
func (k Keeper) InstanceID() []byte {
	return append([]byte(nil), k.instanceID...)
}

// commit runs fn against a child cache. Write runs only after fn returns nil.
// An error discards every exchange write staged by fn.
func (k Keeper) commit(ctx context.Context, fn func(sdk.Context) error) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := fn(cacheCtx); err != nil {
		return err
	}
	if k.failBeforeWrite != nil {
		return k.failBeforeWrite
	}
	write()
	return nil
}

func (k Keeper) kv(ctx context.Context) (store.KVStore, error) {
	return k.store.OpenKVStore(ctx), nil
}

func executionHeight(ctx sdk.Context) (uint64, error) {
	height := ctx.BlockHeight()
	if height < 0 {
		return 0, types.ErrNegativeHeight
	}
	return uint64(height), nil
}

func (k Keeper) get(ctx context.Context, key []byte) ([]byte, error) {
	kv, err := k.kv(ctx)
	if err != nil {
		return nil, err
	}
	bz, err := kv.Get(key)
	if err != nil || bz == nil {
		return bz, err
	}
	out := make([]byte, len(bz))
	copy(out, bz)
	return out, nil
}

func (k Keeper) getUint64(ctx context.Context, key []byte) (uint64, error) {
	bz, err := k.get(ctx, key)
	if err != nil {
		return 0, err
	}
	if bz == nil {
		return 0, nil
	}
	return types.DecodeUint64(bz)
}

func (k Keeper) setUint64(ctx context.Context, key []byte, v uint64) error {
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	return kv.Set(key, types.EncodeUint64(v))
}

// CreateMarket inserts one market. Fee rates live on this record.
// There is no parameter-update path in this milestone.
func (k Keeper) CreateMarket(ctx context.Context, market types.Market) error {
	return k.commit(ctx, func(ctx sdk.Context) error {
		if err := market.Validate(); err != nil {
			return err
		}
		key, err := canonical.EncodeMarketKey(market.ID)
		if err != nil {
			return err
		}
		existing, err := k.get(ctx, key)
		if err != nil {
			return err
		}
		if existing != nil {
			return types.ErrExists
		}
		bz, err := types.EncodeMarket(market)
		if err != nil {
			return err
		}
		kv, err := k.kv(ctx)
		if err != nil {
			return err
		}
		if err := kv.Set(key, bz); err != nil {
			return err
		}
		return k.bumpRevision(ctx)
	})
}

// GetMarket loads one market.
func (k Keeper) GetMarket(ctx context.Context, id domain.MarketID) (types.Market, error) {
	key, err := canonical.EncodeMarketKey(id)
	if err != nil {
		return types.Market{}, err
	}
	bz, err := k.get(ctx, key)
	if err != nil {
		return types.Market{}, err
	}
	if bz == nil {
		return types.Market{}, types.ErrNotFound
	}
	return types.DecodeMarket(bz)
}

// GetOrder loads the active order. The book index is not consulted.
func (k Keeper) GetOrder(ctx context.Context, id domain.OrderID) (types.StoredOrder, error) {
	key, err := canonical.EncodeActiveOrderKey(id)
	if err != nil {
		return types.StoredOrder{}, err
	}
	bz, err := k.get(ctx, key)
	if err != nil {
		return types.StoredOrder{}, err
	}
	if bz == nil {
		return types.StoredOrder{}, types.ErrNotFound
	}
	return types.DecodeOrder(id, bz)
}

// GetTrade loads one trade by market and sequence.
func (k Keeper) GetTrade(ctx context.Context, marketID domain.MarketID, sequence uint64) (types.Trade, error) {
	key, err := canonical.EncodeTradeKey(marketID, sequence)
	if err != nil {
		return types.Trade{}, err
	}
	bz, err := k.get(ctx, key)
	if err != nil {
		return types.Trade{}, err
	}
	if bz == nil {
		return types.Trade{}, types.ErrNotFound
	}
	trade, err := types.DecodeTrade(bz)
	if err != nil {
		return types.Trade{}, err
	}
	if trade.MarketID != marketID || trade.Sequence != sequence {
		return types.Trade{}, types.ErrCorrupt
	}
	return trade, nil
}

// GetRevision returns the exchange revision. A missing key is zero.
func (k Keeper) GetRevision(ctx context.Context) (uint64, error) {
	return k.getUint64(ctx, canonical.EncodeExchangeRevisionKey())
}

// GetCommandNonce returns the last accepted command nonce. A missing key is zero.
func (k Keeper) GetCommandNonce(ctx context.Context, owner []byte) (uint64, error) {
	key, err := canonical.EncodeAccountNonceKey(owner)
	if err != nil {
		return 0, err
	}
	return k.getUint64(ctx, key)
}

// GetOrderSequence returns the last assigned resting-order sequence.
func (k Keeper) GetOrderSequence(ctx context.Context, marketID domain.MarketID) (uint64, error) {
	key, err := canonical.EncodeMarketSequenceKey(marketID)
	if err != nil {
		return 0, err
	}
	return k.getUint64(ctx, key)
}

// GetTradeSequence returns the last assigned trade sequence.
func (k Keeper) GetTradeSequence(ctx context.Context, marketID domain.MarketID) (uint64, error) {
	key, err := canonical.EncodeTradeSequenceKey(marketID)
	if err != nil {
		return 0, err
	}
	return k.getUint64(ctx, key)
}

func (k Keeper) bumpRevision(ctx context.Context) error {
	key := canonical.EncodeExchangeRevisionKey()
	current, err := k.getUint64(ctx, key)
	if err != nil {
		return err
	}
	next, err := arithmetic.Add(current, 1)
	if err != nil {
		return err
	}
	return k.setUint64(ctx, key, next)
}

func (k Keeper) expectedNonce(ctx context.Context, owner []byte) (uint64, error) {
	last, err := k.GetCommandNonce(ctx, owner)
	if err != nil {
		return 0, err
	}
	return domain.ExpectedCommandNonce(last)
}

func (k Keeper) acceptNonce(ctx context.Context, owner []byte, nonce uint64) error {
	expected, err := k.expectedNonce(ctx, owner)
	if err != nil {
		return err
	}
	if nonce != expected {
		return types.ErrWrongNonce
	}
	key, err := canonical.EncodeAccountNonceKey(owner)
	if err != nil {
		return err
	}
	return k.setUint64(ctx, key, nonce)
}
