package keeper

import (
	"bytes"
	"context"
	"errors"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/internal/telemetry"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// placeStats holds wall durations for telemetry. It is not consensus state.
type placeStats struct {
	match  time.Duration
	settle time.Duration
	fills  int
}

// PlaceOrder validates, matches, and settles one command.
// The child cache is written only after the execution plan checks out.
// Expiry uses the block height, not the header timestamp.
func (k Keeper) PlaceOrder(ctx context.Context, cmd types.PlaceOrderCommand) (types.PlaceResult, error) {
	var result types.PlaceResult
	var stats placeStats
	err := k.commit(ctx, func(ctx sdk.Context) error {
		plan, err := k.place(ctx, cmd, &stats)
		if err != nil {
			return err
		}
		if err := k.apply(ctx, plan); err != nil {
			return err
		}
		result = types.PlaceResult{
			OrderID:   plan.taker.Order.ID,
			Fills:     plan.trades,
			Remaining: plan.remaining,
			Rested:    plan.rest,
			Stop:      plan.stop,
		}
		return nil
	})
	if err != nil {
		return types.PlaceResult{}, err
	}
	telemetry.RecordPlace(stats.fills, stats.match, stats.settle)
	return result, nil
}

func (k Keeper) place(ctx sdk.Context, cmd types.PlaceOrderCommand, stats *placeStats) (executionPlan, error) {
	height, err := executionHeight(ctx)
	if err != nil {
		return executionPlan{}, err
	}
	market, err := k.GetMarket(ctx, cmd.MarketID)
	if err != nil {
		return executionPlan{}, err
	}
	if !market.Enabled {
		return executionPlan{}, types.ErrDisabled
	}
	owner := append([]byte(nil), cmd.Owner...)
	if types.IsFeeCollector(owner) {
		return executionPlan{}, types.ErrFeeCollector
	}
	expected, err := k.expectedNonce(ctx, owner)
	if err != nil {
		return executionPlan{}, err
	}
	if cmd.CommandNonce != expected {
		return executionPlan{}, types.ErrWrongNonce
	}

	id, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID:            k.chainID,
		ExchangeInstanceID: k.instanceID,
		Owner:              owner,
		MarketID:           cmd.MarketID,
		CommandNonce:       cmd.CommandNonce,
	})
	if err != nil {
		return executionPlan{}, err
	}
	order := domain.Order{
		ID:                id,
		Owner:             owner,
		MarketID:          cmd.MarketID,
		Side:              cmd.Side,
		Type:              cmd.Type,
		TimeInForce:       cmd.TimeInForce,
		Price:             cmd.Price,
		OriginalQuantity:  cmd.Quantity,
		RemainingQuantity: cmd.Quantity,
		ExpiryHeight:      cmd.ExpiryHeight,
		CommandNonce:      cmd.CommandNonce,
	}
	if err := order.ValidateIncoming(); err != nil {
		return executionPlan{}, err
	}
	if order.ExpiredAt(height) {
		return executionPlan{}, types.ErrExpired
	}
	if _, err := k.GetOrder(ctx, id); err == nil {
		return executionPlan{}, types.ErrExists
	} else if !errors.Is(err, types.ErrNotFound) {
		return executionPlan{}, err
	}
	if len(cmd.ClientOrderID) > 0 {
		key, err := canonical.EncodeActiveClientOrderKey(owner, cmd.ClientOrderID)
		if err != nil {
			return executionPlan{}, err
		}
		existing, err := k.get(ctx, key)
		if err != nil {
			return executionPlan{}, err
		}
		if existing != nil {
			return executionPlan{}, types.ErrExists
		}
	}
	if err := k.requireReserve(ctx, market, order); err != nil {
		return executionPlan{}, err
	}
	if err := k.fail(FailAfterReserve); err != nil {
		return executionPlan{}, err
	}

	src, err := k.openBook(ctx, market.ID, opposite(order.Side))
	if err != nil {
		return executionPlan{}, err
	}
	defer src.Close()

	matchStart := time.Now()
	matched, err := matching.Match(src, matching.MatchInput{
		Incoming:        order,
		ExecutionHeight: height,
		MaxMakerVisits:  market.MaxMakerVisits,
	})
	if stats != nil {
		stats.match = time.Since(matchStart)
	}
	if err != nil {
		return executionPlan{}, err
	}
	if err := assertNotCrossed(src, order, matched); err != nil {
		return executionPlan{}, err
	}
	if err := k.fail(FailAfterMatch); err != nil {
		return executionPlan{}, err
	}
	// Close before any write. Close is idempotent with the defer.
	if err := src.Close(); err != nil {
		return executionPlan{}, err
	}

	switch matched.StopReason {
	case matching.StopReasonFOKRejected:
		return executionPlan{}, types.ErrFOKRejected
	case matching.StopReasonExpired:
		return executionPlan{}, types.ErrExpired
	case matching.StopReasonFilled, matching.StopReasonBookExhausted, matching.StopReasonPriceBoundary, matching.StopReasonSelfTrade, matching.StopReasonVisitLimit:
	default:
		return executionPlan{}, types.ErrSettlement
	}
	settleStart := time.Now()
	plan, err := k.settle(ctx, market, order, matched)
	if stats != nil {
		stats.settle = time.Since(settleStart)
		stats.fills = len(plan.trades)
	}
	if err != nil {
		return executionPlan{}, err
	}
	plan.clientOrderID = append([]byte(nil), cmd.ClientOrderID...)
	return plan, nil
}

// A resting remainder is allowed only when the next opposite order does not
// cross it. Expired makers are not executable; the cursor has already skipped
// those that still cross.
func assertNotCrossed(src *bookSource, taker domain.Order, plan matching.MatchPlan) error {
	if !plan.RestIncoming {
		return nil
	}
	next, ok, err := src.Peek()
	if err != nil {
		return err
	}
	switch plan.StopReason {
	case matching.StopReasonBookExhausted:
		if ok {
			return types.ErrCrossed
		}
		return nil
	case matching.StopReasonPriceBoundary:
		if !ok {
			return types.ErrCrossed
		}
		if taker.Side == domain.SideBuy && next.Price <= taker.Price {
			return types.ErrCrossed
		}
		if taker.Side == domain.SideSell && next.Price >= taker.Price {
			return types.ErrCrossed
		}
		return nil
	default:
		return types.ErrCrossed
	}
}

func opposite(side domain.Side) domain.Side {
	switch side {
	case domain.SideBuy:
		return domain.SideSell
	case domain.SideSell:
		return domain.SideBuy
	default:
		return 0
	}
}

func (k Keeper) apply(ctx sdk.Context, plan executionPlan) error {
	if err := k.fail(FailBeforeBook); err != nil {
		return err
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	updated := false
	noteUpdate := func() error {
		if updated {
			return nil
		}
		updated = true
		return k.fail(FailDuringOrderUpdate)
	}
	for _, maker := range plan.makers {
		if maker.remove {
			if err := k.removeResting(ctx, maker.order); err != nil {
				return err
			}
		} else if err := k.putActive(ctx, maker.order); err != nil {
			return err
		}
		if err := noteUpdate(); err != nil {
			return err
		}
	}
	if plan.rest {
		if err := k.putResting(ctx, plan.taker); err != nil {
			return err
		}
		if len(plan.clientOrderID) > 0 {
			key, err := canonical.EncodeActiveClientOrderKey(plan.owner, plan.clientOrderID)
			if err != nil {
				return err
			}
			if err := kv.Set(key, append([]byte(nil), plan.taker.Order.ID[:]...)); err != nil {
				return err
			}
		}
		if err := noteUpdate(); err != nil {
			return err
		}
	}
	for _, bal := range plan.balances {
		if err := k.setBalance(ctx, bal.owner, bal.asset, bal.bal); err != nil {
			return err
		}
	}
	wroteTrade := false
	for _, trade := range plan.trades {
		key, err := canonical.EncodeTradeKey(trade.MarketID, trade.Sequence)
		if err != nil {
			return err
		}
		bz, err := types.EncodeTrade(trade)
		if err != nil {
			return err
		}
		if err := kv.Set(key, bz); err != nil {
			return err
		}
		if !wroteTrade {
			wroteTrade = true
			if err := k.fail(FailDuringTrade); err != nil {
				return err
			}
		}
	}
	if plan.setOrderSequence {
		key, err := canonical.EncodeMarketSequenceKey(plan.taker.Order.MarketID)
		if err != nil {
			return err
		}
		if err := k.setUint64(ctx, key, plan.orderSequence); err != nil {
			return err
		}
	}
	if plan.setTradeSequence {
		key, err := canonical.EncodeTradeSequenceKey(plan.taker.Order.MarketID)
		if err != nil {
			return err
		}
		if err := k.setUint64(ctx, key, plan.tradeSequence); err != nil {
			return err
		}
	}
	if err := k.fail(FailBeforeNonce); err != nil {
		return err
	}
	if err := k.acceptNonce(ctx, plan.owner, plan.nonce); err != nil {
		return err
	}
	if err := k.fail(FailBeforeRevision); err != nil {
		return err
	}
	if err := k.setUint64(ctx, canonical.EncodeExchangeRevisionKey(), plan.revision); err != nil {
		return err
	}
	emitOrderEvents(ctx, plan)
	return nil
}

// CancelOrder releases the remaining reservation and deletes the order by ID.
func (k Keeper) CancelOrder(ctx context.Context, cmd types.CancelOrderCommand) (types.CancelResult, error) {
	var result types.CancelResult
	err := k.commit(ctx, func(ctx sdk.Context) error {
		var err error
		result, err = k.cancel(ctx, cmd)
		return err
	})
	if err != nil {
		return types.CancelResult{}, err
	}
	telemetry.RecordCancel()
	return result, nil
}

func (k Keeper) cancel(ctx sdk.Context, cmd types.CancelOrderCommand) (types.CancelResult, error) {
	if err := domain.ValidateOwner(cmd.Owner); err != nil {
		return types.CancelResult{}, err
	}
	order, err := k.GetOrder(ctx, cmd.OrderID)
	if err != nil {
		return types.CancelResult{}, err
	}
	owner := append([]byte(nil), cmd.Owner...)
	if !bytes.Equal(order.Order.Owner, owner) {
		return types.CancelResult{}, types.ErrWrongOwner
	}
	expected, err := k.expectedNonce(ctx, owner)
	if err != nil {
		return types.CancelResult{}, err
	}
	if cmd.CommandNonce != expected {
		return types.CancelResult{}, types.ErrWrongNonce
	}
	market, err := k.GetMarket(ctx, order.Order.MarketID)
	if err != nil {
		return types.CancelResult{}, err
	}
	asset, amount, err := reserveOf(market, order.Order)
	if err != nil {
		return types.CancelResult{}, err
	}
	bal, err := k.GetBalance(ctx, owner, asset)
	if err != nil {
		return types.CancelResult{}, err
	}
	bal.Locked, err = arithmetic.Sub(bal.Locked, amount)
	if err != nil {
		return types.CancelResult{}, err
	}
	bal.Available, err = arithmetic.Add(bal.Available, amount)
	if err != nil {
		return types.CancelResult{}, err
	}
	if err := k.fail(FailBeforeBook); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.setBalance(ctx, owner, asset, bal); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.removeResting(ctx, order); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.fail(FailDuringOrderUpdate); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.fail(FailBeforeNonce); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.acceptNonce(ctx, owner, cmd.CommandNonce); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.fail(FailBeforeRevision); err != nil {
		return types.CancelResult{}, err
	}
	if err := k.bumpRevision(ctx); err != nil {
		return types.CancelResult{}, err
	}
	emit(ctx, types.EventTypeOrderCancelled,
		sdk.NewAttribute("order_id", order.Order.ID.String()),
		sdk.NewAttribute("owner", sdk.AccAddress(owner).String()),
		sdk.NewAttribute("asset_id", u64(uint64(asset))),
		sdk.NewAttribute("released", u64(amount)),
	)
	return types.CancelResult{OrderID: order.Order.ID, AssetID: asset, Released: amount}, nil
}
