package keeper

import (
	"errors"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func TestPlaceFailureStagesRollBack(t *testing.T) {
	stages := []string{
		FailAfterReserve,
		FailAfterMatch,
		FailDuringSettle,
		FailBeforeBook,
		FailDuringOrderUpdate,
		FailBeforeNonce,
		FailBeforeRevision,
		FailBeforeCommit,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			k, ctx := setup(t)
			mustCreate(t, k, ctx, testMarket(0, 0))
			buyer := addr(2)
			fund(t, k, ctx, buyer, quoteAsset, 100)
			before := mustDigest(t, k, ctx)
			k.SetFailStageForTest(stage)
			_, err := k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 3, 0))
			if !errors.Is(err, ErrInjected) {
				t.Fatal(err)
			}
			k.SetFailStageForTest("")
			if mustDigest(t, k, ctx) != before {
				t.Fatal("stage committed")
			}
			mustInvariant(t, k, ctx)
			mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 3, 0)
			mustInvariant(t, k, ctx)
		})
	}
}

func TestTradeWriteFailureRollsBack(t *testing.T) {
	k, ctx := setup(t)
	mustCreate(t, k, ctx, testMarket(0, 0))
	seller, buyer := addr(1), addr(2)
	fund(t, k, ctx, seller, baseAsset, 10)
	fund(t, k, ctx, buyer, quoteAsset, 100)
	mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 2, 0)
	before := mustDigest(t, k, ctx)
	k.SetFailStageForTest(FailDuringTrade)
	_, err := k.PlaceOrder(ctx, command(buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 2, 0))
	if !errors.Is(err, ErrInjected) {
		t.Fatal(err)
	}
	k.SetFailStageForTest("")
	if mustDigest(t, k, ctx) != before || nonceOf(t, k, ctx, buyer) != 0 {
		t.Fatal("trade write failure committed")
	}
	mustInvariant(t, k, ctx)
	res := mustPlace(t, k, ctx, buyer, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 2, 0)
	if len(res.Fills) != 1 {
		t.Fatal(res.Fills)
	}
	mustInvariant(t, k, ctx)
}

func TestCancelFailureStagesRollBack(t *testing.T) {
	for _, stage := range []string{FailBeforeBook, FailDuringOrderUpdate, FailBeforeNonce, FailBeforeRevision, FailBeforeCommit} {
		t.Run(stage, func(t *testing.T) {
			k, ctx := setup(t)
			mustCreate(t, k, ctx, testMarket(0, 0))
			seller := addr(1)
			fund(t, k, ctx, seller, baseAsset, 10)
			res := mustPlace(t, k, ctx, seller, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 2, 0)
			before := mustDigest(t, k, ctx)
			k.SetFailStageForTest(stage)
			_, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: seller, OrderID: res.OrderID, CommandNonce: 2})
			if !errors.Is(err, ErrInjected) {
				t.Fatal(err)
			}
			k.SetFailStageForTest("")
			if mustDigest(t, k, ctx) != before {
				t.Fatal("cancel stage committed")
			}
			mustInvariant(t, k, ctx)
			if _, err := k.CancelOrder(ctx, types.CancelOrderCommand{Owner: seller, OrderID: res.OrderID, CommandNonce: 2}); err != nil {
				t.Fatal(err)
			}
			mustInvariant(t, k, ctx)
		})
	}
}
