package keeper

import (
	"math/rand/v2"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestPropertyInvariants(t *testing.T) {
	const seed = uint64(20260928)
	rng := rand.New(rand.NewPCG(seed, 1))
	t.Logf("seed %d", seed)

	k, ctx := setup(t)
	if err := k.initAsset(ctx, types.Asset{ID: baseAsset, Denom: "base"}); err != nil {
		t.Fatal(err)
	}
	if err := k.initAsset(ctx, types.Asset{ID: quoteAsset, Denom: "quote"}); err != nil {
		t.Fatal(err)
	}
	bank := newMemBank()
	k = k.WithBank(bank)
	market := testMarket(1_000, 2_000)
	mustCreate(t, k, ctx, market)

	users := []sdk.AccAddress{addr(1), addr(2), addr(3)}
	for _, user := range users {
		bank.credit(user, "base", 5_000)
		bank.credit(user, "quote", 50_000)
		if err := k.Deposit(ctx, user, sdk.NewInt64Coin("base", 1_000)); err != nil {
			t.Fatal(err)
		}
		if err := k.Deposit(ctx, user, sdk.NewInt64Coin("quote", 10_000)); err != nil {
			t.Fatal(err)
		}
	}
	if err := k.CheckInvariants(ctx); err != nil {
		t.Fatal(err)
	}
	if err := k.CheckCustody(ctx); err != nil {
		t.Fatal(err)
	}

	var rev, orderSeq, tradeSeq uint64
	nonce := map[string]uint64{}
	for step := 0; step < 36; step++ {
		before := mustDigest(t, k, ctx)
		user := users[rng.IntN(len(users))]
		var err error
		switch rng.IntN(6) {
		case 0:
			err = k.Deposit(ctx, user, sdk.NewInt64Coin("base", int64(1+rng.IntN(20))))
		case 1:
			err = k.Withdraw(ctx, user, sdk.NewInt64Coin("quote", int64(1+rng.IntN(30))))
		case 2, 3:
			err = propertyPlace(k, ctx, user, nonce[string(user)]+1, rng)
		case 4:
			err = propertyCancel(t, k, ctx, user, nonce[string(user)]+1)
		default:
			_, err = k.ExpireOrders(ctx, 1_000, 1+rng.IntN(4))
		}
		if err != nil && mustDigest(t, k, ctx) != before {
			t.Fatalf("seed %d step %d err %v changed state", seed, step, err)
		}
		if err := k.CheckInvariants(ctx); err != nil {
			t.Fatalf("seed %d step %d: %v", seed, step, err)
		}
		if err := k.CheckCustody(ctx); err != nil {
			t.Fatalf("seed %d step %d custody: %v", seed, step, err)
		}
		nextRev, err := k.GetRevision(ctx)
		if err != nil || nextRev < rev {
			t.Fatalf("revision %d -> %d %v", rev, nextRev, err)
		}
		nextOrder, err := k.GetOrderSequence(ctx, marketID)
		if err != nil || nextOrder < orderSeq {
			t.Fatalf("order sequence %d -> %d", orderSeq, nextOrder)
		}
		nextTrade, err := k.GetTradeSequence(ctx, marketID)
		if err != nil || nextTrade < tradeSeq {
			t.Fatalf("trade sequence %d -> %d", tradeSeq, nextTrade)
		}
		nextNonce := nonceOf(t, k, ctx, user)
		if nextNonce < nonce[string(user)] {
			t.Fatalf("nonce %d -> %d", nonce[string(user)], nextNonce)
		}
		rev, orderSeq, tradeSeq = nextRev, nextOrder, nextTrade
		nonce[string(user)] = nextNonce
	}
}

func propertyPlace(k Keeper, ctx sdk.Context, user sdk.AccAddress, nonce uint64, rng *rand.Rand) error {
	side := domain.SideBuy
	if rng.IntN(2) == 0 {
		side = domain.SideSell
	}
	typ := domain.OrderTypeLimit
	tif := domain.TimeInForceGTC
	switch rng.IntN(5) {
	case 1:
		tif = domain.TimeInForceIOC
	case 2:
		tif = domain.TimeInForceFOK
	case 3:
		tif = domain.TimeInForceGTD
	case 4:
		typ = domain.OrderTypeMarket
		tif = domain.TimeInForceIOC
	}
	var expiry uint64
	if tif == domain.TimeInForceGTD {
		expiry = 1_000
	}
	_, err := k.PlaceOrder(ctx, types.PlaceOrderCommand{
		Owner:        user,
		MarketID:     marketID,
		Side:         side,
		Type:         typ,
		TimeInForce:  tif,
		Price:        domain.Price(1 + rng.IntN(8)),
		Quantity:     domain.Quantity(1 + rng.IntN(4)),
		ExpiryHeight: expiry,
		CommandNonce: nonce,
	})
	return err
}

func propertyCancel(t *testing.T, k Keeper, ctx sdk.Context, user sdk.AccAddress, nonce uint64) error {
	t.Helper()
	orders, _, err := k.listOpenOrders(ctx, user, marketID, 0, 20)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return nil
	}
	_, err = k.CancelOrder(ctx, types.CancelOrderCommand{
		Owner:        user,
		OrderID:      orders[0].Order.ID,
		CommandNonce: nonce,
	})
	return err
}
