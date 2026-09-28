package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/internal/telemetry"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestRolledBackBatchDoesNotCountCommittedMetrics(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 20)
	e.fund(t, bob.addr, quoteAsset, 100)
	sell, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	buy, _ := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	bad, _ := e.place(t, alice, 9, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 1)
	ctx := e.ctx.WithExecMode(sdk.ExecModeFinalize)
	orders, trades, commands := telemetry.OrdersProcessed(), telemetry.TradesExecuted(), telemetry.BatchCommands()
	if _, err := e.bk.FinalizeBatch(ctx, e.msg(1, 1, sell, buy, bad)); err == nil {
		t.Fatal("expected rollback")
	}
	if _, err := e.ek.GetTrade(e.ctx, mkt, 1); err == nil {
		t.Fatal("rolled-back trade was stored")
	}
	if e.nonce(t, alice.addr) != 0 || e.nonce(t, bob.addr) != 0 {
		t.Fatalf("nonces %d %d", e.nonce(t, alice.addr), e.nonce(t, bob.addr))
	}
	if telemetry.OrdersProcessed() != orders || telemetry.TradesExecuted() != trades || telemetry.BatchCommands() != commands {
		t.Fatalf("rolled-back batch changed committed metrics")
	}

	sim := e.ctx.WithExecMode(sdk.ExecModeSimulate)
	if _, err := e.bk.FinalizeBatch(sim, e.msg(1, 1, sell, buy)); err != nil {
		t.Fatal(err)
	}
	if telemetry.OrdersProcessed() != orders || telemetry.TradesExecuted() != trades || telemetry.BatchCommands() != commands {
		t.Fatal("simulation counted as committed")
	}

	check := setup(t)
	check.fund(t, alice.addr, baseAsset, 20)
	check.fund(t, bob.addr, quoteAsset, 100)
	sell, _ = check.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	buy, _ = check.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	if _, err := check.bk.FinalizeBatch(check.ctx.WithExecMode(sdk.ExecModeCheck), check.msg(1, 1, sell, buy)); err != nil {
		t.Fatal(err)
	}
	if telemetry.TradesExecuted() != trades {
		t.Fatal("check mode counted a trade")
	}

	ok := setup(t)
	ok.fund(t, alice.addr, baseAsset, 20)
	ok.fund(t, bob.addr, quoteAsset, 100)
	sell, _ = ok.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	buy, _ = ok.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	batch, err := ok.bk.FinalizeBatch(ok.ctx.WithExecMode(sdk.ExecModeFinalize), ok.msg(1, 1, sell, buy))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results[1].Trades) != 1 {
		t.Fatalf("%+v", batch.Results)
	}
	if telemetry.OrdersProcessed() != orders+2 || telemetry.TradesExecuted() != trades+1 || telemetry.BatchCommands() != commands+2 {
		t.Fatalf("orders %v trades %v commands %v", telemetry.OrdersProcessed(), telemetry.TradesExecuted(), telemetry.BatchCommands())
	}
	if _, err := ok.ek.GetTrade(ok.ctx, mkt, 1); err != nil {
		t.Fatal(err)
	}
}
