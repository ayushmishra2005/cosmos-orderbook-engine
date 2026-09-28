package keeper

import (
	"context"
	"errors"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// GetBalance returns the atom balance. A missing key is a zero balance.
func (k Keeper) GetBalance(ctx context.Context, owner []byte, asset domain.AssetID) (types.Balance, error) {
	if err := domain.ValidateOwner(owner); err != nil {
		return types.Balance{}, err
	}
	key, err := canonical.EncodeBalanceKey(owner, asset)
	if err != nil {
		return types.Balance{}, err
	}
	bz, err := k.get(ctx, key)
	if err != nil {
		return types.Balance{}, err
	}
	if bz == nil {
		return types.Balance{}, nil
	}
	return types.DecodeBalance(bz)
}

func (k Keeper) setBalance(ctx context.Context, owner []byte, asset domain.AssetID, bal types.Balance) error {
	if err := types.ValidateBalanceCapacity(bal.Available, bal.Locked); err != nil {
		return err
	}
	key, err := canonical.EncodeBalanceKey(owner, asset)
	if err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	if bal.Available == 0 && bal.Locked == 0 {
		return kv.Delete(key)
	}
	return kv.Set(key, types.EncodeBalance(bal))
}

// reserveOf is the atom lock for an order's remaining quantity.
// Buys lock quote at the order's own tick. Sells lock base.
func reserveOf(market types.Market, order domain.Order) (domain.AssetID, uint64, error) {
	switch order.Side {
	case domain.SideBuy:
		amount, err := arithmetic.Notional(order.RemainingQuantity, order.Price, market.QuoteAtomsPerTickPerLot)
		return market.QuoteAssetID, amount, err
	case domain.SideSell:
		amount, err := arithmetic.BaseAmount(order.RemainingQuantity, market.BaseLotSize)
		return market.BaseAssetID, amount, err
	default:
		return 0, 0, domain.ErrInvalidSide
	}
}

func (k Keeper) requireReserve(ctx context.Context, market types.Market, order domain.Order) error {
	asset, amount, err := reserveOf(market, order)
	if err != nil {
		return err
	}
	bal, err := k.GetBalance(ctx, order.Owner, asset)
	if err != nil {
		return err
	}
	if _, err := arithmetic.Sub(bal.Available, amount); err != nil {
		if errors.Is(err, arithmetic.ErrUnderflow) {
			return types.ErrInsufficientBalance
		}
		return err
	}
	return nil
}
