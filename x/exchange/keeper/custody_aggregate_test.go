package keeper

import (
	"math"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func TestCustodyAggregatesPerAssetBeyondUint64(t *testing.T) {
	bank := newIntBank()
	k, ctx := setup(t)
	k = k.WithBank(bank)
	if err := k.initAsset(ctx, types.Asset{ID: baseAsset, Denom: "base"}); err != nil {
		t.Fatal(err)
	}
	if err := k.initAsset(ctx, types.Asset{ID: quoteAsset, Denom: "quote"}); err != nil {
		t.Fatal(err)
	}
	writeBal(t, k, ctx, addr(1), baseAsset, math.MaxUint64, 0)
	writeBal(t, k, ctx, addr(2), baseAsset, math.MaxUint64, 0)
	writeBal(t, k, ctx, types.FeeCollectorOwner, baseAsset, 5, 0)
	writeBal(t, k, ctx, addr(3), quoteAsset, math.MaxUint64, 0)

	baseSum := sdkmath.NewIntFromUint64(math.MaxUint64).MulRaw(2).AddRaw(5)
	quoteSum := sdkmath.NewIntFromUint64(math.MaxUint64)
	gotBase, err := k.SumLiabilities(ctx, baseAsset)
	if err != nil || !gotBase.Equal(baseSum) {
		t.Fatalf("base %s %v", gotBase, err)
	}
	gotQuote, err := k.SumLiabilities(ctx, quoteAsset)
	if err != nil || !gotQuote.Equal(quoteSum) {
		t.Fatalf("quote %s %v", gotQuote, err)
	}
	if gotBase.Equal(gotQuote) || gotBase.Equal(baseSum.Add(quoteSum)) {
		t.Fatal("assets shared an accumulator")
	}

	bank.credit(ModuleAddress(), "base", baseSum)
	bank.credit(ModuleAddress(), "quote", quoteSum)
	if err := k.CheckCustody(ctx); err != nil {
		t.Fatal(err)
	}
	bank.credit(ModuleAddress(), "base", baseSum.SubRaw(1))
	if err := k.CheckCustody(ctx); err == nil {
		t.Fatal("short bank backing was accepted")
	}

	writeBal(t, k, ctx, addr(4), baseAsset, math.MaxUint64, 1)
	if _, err := k.SumLiabilities(ctx, baseAsset); err == nil {
		t.Fatal("owner capacity overflow was accepted")
	}
}

func TestGenesisBackingUsesWidePerAssetSums(t *testing.T) {
	sum := sdkmath.NewIntFromUint64(math.MaxUint64).MulRaw(2)
	gs := *v1.DefaultGenesis()
	gs.Balances = []*v1.GenesisBalance{
		{Owner: sdk.AccAddress(addr(1)).String(), AssetId: 1, Available: math.MaxUint64},
		{Owner: sdk.AccAddress(addr(2)).String(), AssetId: 1, Available: math.MaxUint64},
	}
	if err := gs.Validate(); err != nil {
		t.Fatal(err)
	}
	short, ctx := setupGenesisKeeper(t, staticBank{amount: sum.SubRaw(1)})
	if err := short.InitGenesis(ctx, gs); err == nil {
		t.Fatal("short genesis backing was accepted")
	}
	k, ctx := setupGenesisKeeper(t, staticBank{amount: sum})
	if err := k.InitGenesis(ctx, gs); err != nil {
		t.Fatal(err)
	}
	got, err := k.SumLiabilities(ctx, baseAsset)
	if err != nil || !got.Equal(sum) {
		t.Fatalf("imported %s %v", got, err)
	}
	exported, err := k.ExportGenesis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, againCtx := setupGenesisKeeper(t, staticBank{amount: sum})
	if err := again.InitGenesis(againCtx, exported); err != nil {
		t.Fatal(err)
	}
	got, err = again.SumLiabilities(againCtx, baseAsset)
	if err != nil || !got.Equal(sum) {
		t.Fatalf("reimported %s %v", got, err)
	}
	overflow := *v1.DefaultGenesis()
	overflow.Balances = []*v1.GenesisBalance{{
		Owner: sdk.AccAddress(addr(3)).String(), AssetId: 1, Available: math.MaxUint64, Locked: 1,
	}}
	if err := overflow.Validate(); err == nil {
		t.Fatal("per-owner overflow passed genesis validation")
	}
}

func writeBal(t *testing.T, k Keeper, ctx sdk.Context, owner []byte, asset domain.AssetID, available, locked uint64) {
	t.Helper()
	key, err := canonical.EncodeBalanceKey(owner, asset)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := k.kv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Set(key, types.EncodeBalance(types.Balance{Available: available, Locked: locked})); err != nil {
		t.Fatal(err)
	}
}
