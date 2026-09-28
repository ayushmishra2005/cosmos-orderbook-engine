package keeper

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func (e env) digests(t *testing.T) (exchange, batch [32]byte) {
	t.Helper()
	var err error
	exchange, err = e.ek.SnapshotDigest(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = e.bk.SnapshotDigest(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return exchange, batch
}

func TestBatchInvariantsAndFailureStages(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 100)
	e.fund(t, bob.addr, quoteAsset, 10_000)
	pb1, c1 := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4)
	pb2, c2 := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 5, 4)
	batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), pb1, pb2))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.bk.CheckInvariants(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.ek.CheckInvariants(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := BatchIDMatches(batch.Number, batch.PreRevision, batch.ID, []canonical.Command{c1, c2}); err != nil {
		t.Fatal(err)
	}
	if err := CommandsEqual(batch.Results, []canonical.Command{c1, c2}); err != nil {
		t.Fatal(err)
	}

	for _, stage := range []string{"after-1", "after-n", "before-record", "before-commit"} {
		t.Run(stage, func(t *testing.T) {
			env := setup(t)
			env.fund(t, alice.addr, baseAsset, 20)
			env.fund(t, bob.addr, quoteAsset, 200)
			a, _ := env.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 2)
			b, _ := env.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1)
			ex, batchDigest := env.digests(t)
			switch stage {
			case "after-1":
				env.bk.failAfterCommands = 1
			case "after-n":
				env.bk.failAfterCommands = 2
			case "before-record":
				env.bk.failBeforeRecord = true
			case "before-commit":
				env.bk.failBeforeWrite = errInjected
			}
			if _, err := env.bk.FinalizeBatch(env.ctx, env.msg(1, env.revision(t), a, b)); !errors.Is(err, errInjected) {
				t.Fatal(err)
			}
			gotEx, gotBatch := env.digests(t)
			if gotEx != ex || gotBatch != batchDigest {
				t.Fatal("batch stage committed")
			}
			if err := env.bk.CheckInvariants(env.ctx); err != nil {
				t.Fatal(err)
			}
			if err := env.ek.CheckInvariants(env.ctx); err != nil {
				t.Fatal(err)
			}
			env.noBatch(t)
		})
	}
}

func TestDirectAndBatchNonceAndRevision(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 20)
	if _, err := e.ek.PlaceOrder(e.ctx, exchangetypes.PlaceOrderCommand{
		Owner: alice.addr, MarketID: mkt, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 4, Quantity: 2, CommandNonce: 1,
	}); err != nil {
		t.Fatal(err)
	}
	stale := e.revision(t) - 1
	ex, batchDigest := e.digests(t)
	pb, _ := e.place(t, alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, stale, pb)); !errors.Is(err, types.ErrStaleRevision) {
		t.Fatal(err)
	}
	gotEx, gotBatch := e.digests(t)
	if gotEx != ex || gotBatch != batchDigest {
		t.Fatal("stale revision committed")
	}

	pb, _ = e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), pb)); !errors.Is(err, exchangetypes.ErrWrongNonce) {
		t.Fatal(err)
	}
	if e.nonce(t, alice.addr) != 1 {
		t.Fatal(e.nonce(t, alice.addr))
	}
	pb, _ = e.place(t, alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), pb)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ek.PlaceOrder(e.ctx, exchangetypes.PlaceOrderCommand{
		Owner: alice.addr, MarketID: mkt, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 7, Quantity: 1, CommandNonce: 2,
	}); !errors.Is(err, exchangetypes.ErrWrongNonce) {
		t.Fatal(err)
	}
	if err := e.bk.CheckInvariants(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.ek.CheckInvariants(e.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBatchLimitAndNumberExhaustion(t *testing.T) {
	e := setup(t)
	ex, batchDigest := e.digests(t)
	tooMany := e.msg(1, e.revision(t))
	tooMany.Commands = make([]*v1.SignedCommand, types.MaxCommands+1)
	if _, err := e.bk.FinalizeBatch(e.ctx, tooMany); !errors.Is(err, types.ErrLimit) {
		t.Fatal(err)
	}
	gotEx, gotBatch := e.digests(t)
	if gotEx != ex || gotBatch != batchDigest {
		t.Fatal("oversized batch committed")
	}

	params, err := e.bk.GetParams(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	params.Latest = math.MaxUint64
	if err := e.bk.setParams(e.ctx, params); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(0, e.revision(t))); !errors.Is(err, arithmetic.ErrOverflow) {
		t.Fatal(err)
	}
	params, err = e.bk.GetParams(e.ctx)
	if err != nil || params.Latest != math.MaxUint64 {
		t.Fatal(params.Latest, err)
	}
}

func TestBrokenBatchCommitmentChain(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	first, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), first)); err != nil {
		t.Fatal(err)
	}
	second, _ := e.place(t, alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(2, e.revision(t), second)); err != nil {
		t.Fatal(err)
	}
	batch, err := e.bk.GetBatch(e.ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	batch.Previous = types.BatchCommitment{9}
	hash, commitment, err := e.bk.batchHashes(batch)
	if err != nil {
		t.Fatal(err)
	}
	batch.ResultsHash = hash
	batch.Commitment = commitment
	bz, err := types.EncodeBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := e.bk.kv(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Set(types.BatchKey(2), bz); err != nil {
		t.Fatal(err)
	}
	params, err := e.bk.GetParams(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	params.Head = commitment
	if err := e.bk.setParams(e.ctx, params); err != nil {
		t.Fatal(err)
	}
	if err := e.bk.CheckInvariants(e.ctx); !errors.Is(err, types.ErrCorrupt) {
		t.Fatal(err)
	}
	got, err := e.bk.GetBatch(e.ctx, 2)
	if err != nil || got.Previous != batch.Previous {
		t.Fatal("commitment chain repaired")
	}
}

func TestExchangeFailureInsideBatchRollsBack(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	pb, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 3, 1)
	ex, batchDigest := e.digests(t)
	e.ek.SetFailStageForTest(exchangekeeper.FailDuringSettle)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), pb)); !errors.Is(err, exchangekeeper.ErrInjected) {
		t.Fatal(err)
	}
	e.ek.SetFailStageForTest("")
	gotEx, gotBatch := e.digests(t)
	if gotEx != ex || gotBatch != batchDigest {
		t.Fatal("exchange failure inside a batch committed")
	}
	e.noBatch(t)
	if e.nonce(t, alice.addr) != 0 {
		t.Fatal(e.nonce(t, alice.addr))
	}
}

func TestBatchProperty(t *testing.T) {
	const seed = uint64(20260928)
	rng := rand.New(rand.NewPCG(seed, 1))
	t.Logf("seed %d", seed)
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 500)
	e.fund(t, alice.addr, quoteAsset, 5_000)
	e.fund(t, bob.addr, baseAsset, 500)
	e.fund(t, bob.addr, quoteAsset, 5_000)
	traders := []trader{alice, bob}
	nonce := map[string]uint64{}
	var open []struct {
		owner trader
		id    domain.OrderID
	}
	var number uint64
	for step := 0; step < 10; step++ {
		ex, batchDigest := e.digests(t)
		who := traders[rng.IntN(len(traders))]
		var cmds []*v1.SignedCommand
		if len(open) > 0 && rng.IntN(4) == 0 {
			item := open[0]
			who = item.owner
			pb, _ := e.cancel(t, item.owner, nonce[string(item.owner.addr)]+1, item.id)
			cmds = []*v1.SignedCommand{pb}
		} else {
			side := domain.SideSell
			if rng.IntN(2) == 0 {
				side = domain.SideBuy
			}
			pb, _ := e.place(t, who, nonce[string(who.addr)]+1, side, domain.OrderTypeLimit, domain.TimeInForceGTC, uint64(1+rng.IntN(6)), uint64(1+rng.IntN(3)))
			cmds = []*v1.SignedCommand{pb}
		}
		number++
		batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(number, e.revision(t), cmds...))
		if err != nil {
			number--
			gotEx, gotBatch := e.digests(t)
			if gotEx != ex || gotBatch != batchDigest {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
		} else {
			nonce[string(who.addr)]++
			switch batch.Results[0].Status {
			case types.StatusResting:
				open = append(open, struct {
					owner trader
					id    domain.OrderID
				}{who, batch.Results[0].OrderID})
			case types.StatusCancelled:
				if len(open) > 0 {
					open = open[1:]
				}
			}
		}
		if err := e.ek.CheckInvariants(e.ctx); err != nil {
			t.Fatalf("seed %d step %d exchange: %v", seed, step, err)
		}
		if err := e.bk.CheckInvariants(e.ctx); err != nil {
			t.Fatalf("seed %d step %d batch: %v", seed, step, err)
		}
	}
}
