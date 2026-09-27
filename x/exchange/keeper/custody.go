package keeper

import (
	"context"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// BankKeeper is the bank surface used for custody.
// Trades do not call it.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// Deposit moves bank coins onto the exchange module account and credits available balance.
func (k Keeper) Deposit(ctx context.Context, owner sdk.AccAddress, coin sdk.Coin) error {
	return k.commit(ctx, func(ctx sdk.Context) error {
		return k.deposit(ctx, owner, coin)
	})
}

// Withdraw debits available balance and returns bank coins to the owner.
// Locked balance cannot be withdrawn. A bank failure discards the debit.
func (k Keeper) Withdraw(ctx context.Context, owner sdk.AccAddress, coin sdk.Coin) error {
	return k.commit(ctx, func(ctx sdk.Context) error {
		return k.withdraw(ctx, owner, coin)
	})
}

func (k Keeper) deposit(ctx sdk.Context, owner sdk.AccAddress, coin sdk.Coin) error {
	if k.bank == nil {
		return types.ErrCorrupt
	}
	amount, err := atomsOf(coin)
	if err != nil {
		return err
	}
	asset, err := k.AssetByDenom(ctx, coin.Denom)
	if err != nil {
		return err
	}
	if err := k.bank.SendCoinsFromAccountToModule(ctx, owner, types.ModuleName, sdk.NewCoins(sdk.NewCoin(coin.Denom, sdkmath.NewIntFromUint64(amount)))); err != nil {
		return err
	}
	bal, err := k.GetBalance(ctx, owner, asset.ID)
	if err != nil {
		return err
	}
	bal.Available, err = arithmetic.Add(bal.Available, amount)
	if err != nil {
		return err
	}
	if err := k.setBalance(ctx, owner, asset.ID, bal); err != nil {
		return err
	}
	if err := k.bumpRevision(ctx); err != nil {
		return err
	}
	emit(ctx, types.EventTypeDeposit,
		sdk.NewAttribute("owner", owner.String()),
		sdk.NewAttribute("asset_id", u64(uint64(asset.ID))),
		sdk.NewAttribute("amount", u64(amount)),
	)
	return nil
}

func (k Keeper) withdraw(ctx sdk.Context, owner sdk.AccAddress, coin sdk.Coin) error {
	if k.bank == nil {
		return types.ErrCorrupt
	}
	amount, err := atomsOf(coin)
	if err != nil {
		return err
	}
	asset, err := k.AssetByDenom(ctx, coin.Denom)
	if err != nil {
		return err
	}
	bal, err := k.GetBalance(ctx, owner, asset.ID)
	if err != nil {
		return err
	}
	next, err := arithmetic.Sub(bal.Available, amount)
	if err != nil {
		return types.ErrInsufficientBalance
	}
	bal.Available = next
	if err := k.setBalance(ctx, owner, asset.ID, bal); err != nil {
		return err
	}
	if err := k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, owner, sdk.NewCoins(sdk.NewCoin(coin.Denom, sdkmath.NewIntFromUint64(amount)))); err != nil {
		return err
	}
	if err := k.bumpRevision(ctx); err != nil {
		return err
	}
	emit(ctx, types.EventTypeWithdraw,
		sdk.NewAttribute("owner", owner.String()),
		sdk.NewAttribute("asset_id", u64(uint64(asset.ID))),
		sdk.NewAttribute("amount", u64(amount)),
	)
	return nil
}

func atomsOf(coin sdk.Coin) (uint64, error) {
	if err := coin.Validate(); err != nil {
		return 0, types.ErrInvalidAmount
	}
	if !coin.Amount.IsPositive() || !coin.Amount.IsUint64() {
		return 0, types.ErrInvalidAmount
	}
	return coin.Amount.Uint64(), nil
}

// GetAsset loads one registered asset.
func (k Keeper) GetAsset(ctx context.Context, id domain.AssetID) (types.Asset, error) {
	key, err := canonical.EncodeAssetKey(id)
	if err != nil {
		return types.Asset{}, err
	}
	bz, err := k.get(ctx, key)
	if err != nil {
		return types.Asset{}, err
	}
	if bz == nil {
		return types.Asset{}, types.ErrNotFound
	}
	asset, err := types.DecodeAsset(bz)
	if err != nil {
		return types.Asset{}, err
	}
	if asset.ID != id {
		return types.Asset{}, types.ErrCorrupt
	}
	return asset, nil
}

// AssetByDenom resolves a bank denom to its registered asset.
func (k Keeper) AssetByDenom(ctx context.Context, denom string) (types.Asset, error) {
	key, err := canonical.EncodeAssetDenomKey(denom)
	if err != nil {
		return types.Asset{}, types.ErrUnknownDenom
	}
	bz, err := k.get(ctx, key)
	if err != nil {
		return types.Asset{}, err
	}
	if bz == nil {
		return types.Asset{}, types.ErrUnknownDenom
	}
	id, err := types.DecodeUint64(bz)
	if err != nil {
		return types.Asset{}, err
	}
	return k.GetAsset(ctx, domain.AssetID(id))
}

func (k Keeper) initAsset(ctx context.Context, asset types.Asset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	idKey, err := canonical.EncodeAssetKey(asset.ID)
	if err != nil {
		return err
	}
	denomKey, err := canonical.EncodeAssetDenomKey(asset.Denom)
	if err != nil {
		return err
	}
	if existing, err := k.get(ctx, idKey); err != nil {
		return err
	} else if existing != nil {
		return types.ErrExists
	}
	if existing, err := k.get(ctx, denomKey); err != nil {
		return err
	} else if existing != nil {
		return types.ErrExists
	}
	bz, err := types.EncodeAsset(asset)
	if err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	if err := kv.Set(idKey, bz); err != nil {
		return err
	}
	return kv.Set(denomKey, types.EncodeUint64(uint64(asset.ID)))
}

func (k Keeper) initMarket(ctx context.Context, market types.Market) error {
	if err := market.Validate(); err != nil {
		return err
	}
	if _, err := k.GetAsset(ctx, market.BaseAssetID); err != nil {
		return err
	}
	if _, err := k.GetAsset(ctx, market.QuoteAssetID); err != nil {
		return err
	}
	key, err := canonical.EncodeMarketKey(market.ID)
	if err != nil {
		return err
	}
	if existing, err := k.get(ctx, key); err != nil {
		return err
	} else if existing != nil {
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
	return kv.Set(key, bz)
}

// SumLiabilities is the sum of available and locked atoms for one asset.
func (k Keeper) SumLiabilities(ctx context.Context, assetID domain.AssetID) (uint64, error) {
	if assetID == 0 {
		return 0, domain.ErrInvalidAsset
	}
	var sum uint64
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixBalance}, func(key, value []byte) (bool, error) {
		_, id, err := canonical.DecodeBalanceKey(key)
		if err != nil {
			return false, err
		}
		if id != assetID {
			return false, nil
		}
		bal, err := types.DecodeBalance(value)
		if err != nil {
			return false, err
		}
		next, err := arithmetic.Add(bal.Available, bal.Locked)
		if err != nil {
			return false, err
		}
		sum, err = arithmetic.Add(sum, next)
		return false, err
	})
	return sum, err
}

// ModuleAddress is the custody account that holds bank coins.
func ModuleAddress() sdk.AccAddress {
	return authtypes.NewModuleAddress(types.ModuleName)
}
