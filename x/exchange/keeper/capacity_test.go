package keeper

import (
	"context"
	"errors"
	"math"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestDepositRejectsBalanceAboveCapacity(t *testing.T) {
	k, ctx := setup(t)
	if err := k.initAsset(ctx, types.Asset{ID: baseAsset, Denom: "base"}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, k, ctx, testMarket(0, 0))
	owner := sdk.AccAddress(addr(1))
	bank := newIntBank()
	bank.credit(owner, "base", sdkmath.NewIntFromUint64(math.MaxUint64).Add(sdkmath.OneInt()))
	k = k.WithBank(bank)

	if err := k.Deposit(ctx, owner, sdk.NewCoin("base", sdkmath.OneInt())); err != nil {
		t.Fatal(err)
	}
	res := mustPlace(t, k, ctx, owner, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTD, 100, 1, 20)
	if !res.Rested {
		t.Fatal("gtd sell did not rest")
	}
	beforeInternal := bal(t, k, ctx, owner, baseAsset)
	beforeBank := bank.get(owner, "base")
	beforeModule := bank.get(ModuleAddress(), "base")
	if beforeInternal.Available != 0 || beforeInternal.Locked != 1 {
		t.Fatalf("locked sell %+v", beforeInternal)
	}

	err := k.Deposit(ctx, owner, sdk.NewCoin("base", sdkmath.NewIntFromUint64(math.MaxUint64)))
	if !errors.Is(err, arithmetic.ErrOverflow) {
		t.Fatalf("deposit: %v", err)
	}
	if got := bal(t, k, ctx, owner, baseAsset); got != beforeInternal {
		t.Fatalf("internal changed %+v", got)
	}
	if !bank.get(owner, "base").Equal(beforeBank) || !bank.get(ModuleAddress(), "base").Equal(beforeModule) {
		t.Fatalf("bank changed owner %s module %s", bank.get(owner, "base"), bank.get(ModuleAddress(), "base"))
	}

	n, err := k.ExpireOrders(ctx, 20, 16)
	if err != nil || n != 1 {
		t.Fatalf("expire n=%d err=%v", n, err)
	}
	after := bal(t, k, ctx, owner, baseAsset)
	if after.Locked != 0 || after.Available != 1 {
		t.Fatalf("after expiry %+v", after)
	}
	if err := k.Deposit(ctx, owner, sdk.NewCoin("base", sdkmath.OneInt())); err != nil {
		t.Fatal(err)
	}
	if got := bal(t, k, ctx, owner, baseAsset); got.Available != 2 || got.Locked != 0 {
		t.Fatalf("later deposit %+v", got)
	}
}

func TestBalanceCapacityBoundaries(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)

	t.Run("cancel at sum max", func(t *testing.T) {
		fund(t, k, ctx, seller, baseAsset, math.MaxUint64)
		res := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 50, 1, 0)
		got := bal(t, k, ctx, seller, baseAsset)
		if got.Available != math.MaxUint64-1 || got.Locked != 1 {
			t.Fatalf("reserved %+v", got)
		}
		if _, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: seller, OrderID: res.OrderID, CommandNonce: 2}); err != nil {
			t.Fatal(err)
		}
		got = bal(t, k, ctx, seller, baseAsset)
		if got.Available != math.MaxUint64 || got.Locked != 0 {
			t.Fatalf("released %+v", got)
		}
	})

	t.Run("price improvement at sum max", func(t *testing.T) {
		fund(t, k, ctx, seller, baseAsset, 1)
		fund(t, k, ctx, buyer, quoteAsset, math.MaxUint64)
		mustPlace(t, k, ctx, seller, 3, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 90, 1, 0)
		res := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 100, 1, 0)
		if len(res.Fills) != 1 || res.Rested {
			t.Fatalf("fill %+v", res)
		}
		got := bal(t, k, ctx, buyer, quoteAsset)
		if got.Locked != 0 || got.Available != math.MaxUint64-90 {
			t.Fatalf("buyer quote %+v", got)
		}
	})

	t.Run("settlement credit rejected at capacity", func(t *testing.T) {
		fund(t, k, ctx, seller, quoteAsset, math.MaxUint64)
		fund(t, k, ctx, seller, baseAsset, 1)
		fund(t, k, ctx, buyer, quoteAsset, 10)
		sell := mustPlace(t, k, ctx, seller, 4, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 1, 1, 0)
		_, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
			Owner: buyer, MarketID: marketID, Side: domain.SideBuy,
			Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceIOC,
			Price: 1, Quantity: 1, CommandNonce: 2,
		})
		if !errors.Is(err, arithmetic.ErrOverflow) {
			t.Fatalf("buy: %v", err)
		}
		if _, err := k.GetOrder(ctx, sell.OrderID); err != nil {
			t.Fatal(err)
		}
		if got := bal(t, k, ctx, seller, quoteAsset); got.Available != math.MaxUint64 || got.Locked != 0 {
			t.Fatalf("seller quote %+v", got)
		}
		if got := bal(t, k, ctx, buyer, quoteAsset); got.Available != 10 || got.Locked != 0 {
			t.Fatalf("buyer quote %+v", got)
		}
	})

	t.Run("maximum available balance", func(t *testing.T) {
		if err := k.initAsset(ctx, types.Asset{ID: baseAsset, Denom: "base"}); err != nil && !errors.Is(err, types.ErrExists) {
			t.Fatal(err)
		}
		who := sdk.AccAddress(addr(9))
		bank := newIntBank()
		bank.credit(who, "base", sdkmath.NewIntFromUint64(math.MaxUint64))
		k = k.WithBank(bank)
		if err := k.Deposit(ctx, who, sdk.NewCoin("base", sdkmath.NewIntFromUint64(math.MaxUint64))); err != nil {
			t.Fatal(err)
		}
		got := bal(t, k, ctx, who, baseAsset)
		if got.Available != math.MaxUint64 || got.Locked != 0 {
			t.Fatalf("%+v", got)
		}
	})
}

type intBank struct {
	bal map[string]sdkmath.Int
}

func newIntBank() *intBank {
	return &intBank{bal: map[string]sdkmath.Int{}}
}

func (b *intBank) credit(addr sdk.AccAddress, denom string, amount sdkmath.Int) {
	b.bal[bankKey(addr, denom)] = amount
}

func (b *intBank) get(addr sdk.AccAddress, denom string) sdkmath.Int {
	amt := b.bal[bankKey(addr, denom)]
	if amt.IsNil() {
		return sdkmath.ZeroInt()
	}
	return amt
}

func (b *intBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.Coin{Denom: denom, Amount: b.get(addr, denom)}
}

func (b *intBank) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	if recipientModule != types.ModuleName {
		return errors.New("bank: module")
	}
	return b.move(sender, ModuleAddress(), amt)
}

func (b *intBank) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipient sdk.AccAddress, amt sdk.Coins) error {
	if senderModule != types.ModuleName {
		return errors.New("bank: module")
	}
	return b.move(ModuleAddress(), recipient, amt)
}

func (b *intBank) move(from, to sdk.AccAddress, amt sdk.Coins) error {
	next := make(map[string]sdkmath.Int, len(b.bal))
	for key, value := range b.bal {
		next[key] = value
	}
	for _, coin := range amt {
		if !coin.Amount.IsPositive() {
			return errors.New("bank: amount")
		}
		src := bankKey(from, coin.Denom)
		have := next[src]
		if have.IsNil() || have.LT(coin.Amount) {
			return errors.New("bank: insufficient funds")
		}
		next[src] = have.Sub(coin.Amount)
		dst := bankKey(to, coin.Denom)
		cur := next[dst]
		if cur.IsNil() {
			cur = sdkmath.ZeroInt()
		}
		next[dst] = cur.Add(coin.Amount)
	}
	b.bal = next
	return nil
}
