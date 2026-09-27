package matching

import (
	"bytes"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

// Match plans fills for one incoming order against an ordered maker cursor.
//
// The resting order is the maker and the incoming order is the taker.
// Execution price is the maker price. Buys walk asks from the lowest price
// and stop when the ask is above the buy tick. Sells walk bids from the
// highest price and stop when the bid is below the sell tick.
//
// Self-trade handling is fixed: when the next eligible maker has the same
// owner, earlier third-party fills are kept and the taker remainder is
// cancelled. A FOK order that cannot fully execute, including because of
// self-trade or the visit cap, produces no fills.
//
// Expired makers are skipped and are not reported on the plan. Removing them
// belongs to the keeper. The plan itself does not depend on map iteration,
// goroutines, wall-clock time, or randomness.
func Match(src OrderSource, in MatchInput) (MatchPlan, error) {
	if src == nil {
		return MatchPlan{}, ErrNilSource
	}
	if err := in.Incoming.ValidateIncoming(); err != nil {
		return MatchPlan{}, err
	}
	if in.Incoming.ExpiredAt(in.ExecutionHeight) {
		plan := MatchPlan{
			RemainingQuantity: in.Incoming.RemainingQuantity,
			StopReason:        StopReasonExpired,
		}
		if err := checkInvariant(in.Incoming.RemainingQuantity, plan); err != nil {
			return MatchPlan{}, err
		}
		return plan, nil
	}

	plan, err := matchCrossing(src, in)
	if err != nil {
		return MatchPlan{}, err
	}
	// FOK is atomic. A simulated partial result is discarded, including when
	// self-trade or the visit cap stopped the scan before the order was filled.
	if in.Incoming.TimeInForce == domain.TimeInForceFOK && plan.RemainingQuantity != 0 {
		plan = MatchPlan{
			RemainingQuantity: in.Incoming.RemainingQuantity,
			StopReason:        StopReasonFOKRejected,
		}
		if err := checkInvariant(in.Incoming.RemainingQuantity, plan); err != nil {
			return MatchPlan{}, err
		}
	}
	return plan, nil
}

func matchCrossing(src OrderSource, in MatchInput) (MatchPlan, error) {
	remaining := in.Incoming.RemainingQuantity
	var fills []Fill
	var visits uint64
	var stop StopReason

	for remaining > 0 {
		if in.MaxMakerVisits > 0 && visits >= uint64(in.MaxMakerVisits) {
			stop = StopReasonVisitLimit
			break
		}
		maker, ok, err := src.Peek()
		if err != nil {
			return MatchPlan{}, err
		}
		if !ok {
			stop = StopReasonBookExhausted
			break
		}
		visits++

		if err := validateMaker(in.Incoming, maker); err != nil {
			return MatchPlan{}, err
		}
		if maker.ExpiredAt(in.ExecutionHeight) {
			if err := src.Next(); err != nil {
				return MatchPlan{}, err
			}
			continue
		}
		// A maker outside the limit does not cross, even if it is the same owner.
		if !priceCrosses(in.Incoming, maker) {
			stop = StopReasonPriceBoundary
			break
		}
		if bytes.Equal(maker.Owner, in.Incoming.Owner) {
			stop = StopReasonSelfTrade
			break
		}

		qty := maker.RemainingQuantity
		if remaining < qty {
			qty = remaining
		}
		fills = append(fills, Fill{
			MakerOrderID: maker.ID,
			TakerOrderID: in.Incoming.ID,
			Price:        maker.Price,
			Quantity:     qty,
		})
		next, err := arithmetic.Sub(uint64(remaining), uint64(qty))
		if err != nil {
			return MatchPlan{}, err
		}
		remaining = domain.Quantity(next)
		// Advance even on a partial maker fill. That fill exhausts the taker,
		// and the maker's book key stays put because its sequence does not change.
		if err := src.Next(); err != nil {
			return MatchPlan{}, err
		}
		if remaining == 0 {
			stop = StopReasonFilled
			break
		}
	}
	if stop == 0 {
		return MatchPlan{}, ErrInvariant
	}

	plan := MatchPlan{
		Fills:             fills,
		RemainingQuantity: remaining,
		StopReason:        stop,
		RestIncoming:      shouldRest(in.Incoming, stop, remaining),
	}
	if err := checkInvariant(in.Incoming.RemainingQuantity, plan); err != nil {
		return MatchPlan{}, err
	}
	return plan, nil
}

func shouldRest(order domain.Order, stop StopReason, remaining domain.Quantity) bool {
	if remaining == 0 || order.Type != domain.OrderTypeLimit {
		return false
	}
	if order.TimeInForce != domain.TimeInForceGTC && order.TimeInForce != domain.TimeInForceGTD {
		return false
	}
	// Resting is safe only when the cursor stopped on a maker that does not
	// cross, or when no maker remains. Visit limit and self-trade can leave
	// a still-crossing book, so those remainders are cancelled.
	return stop == StopReasonBookExhausted || stop == StopReasonPriceBoundary
}

func validateMaker(taker, maker domain.Order) error {
	if err := maker.ValidateResting(); err != nil {
		return err
	}
	if maker.MarketID != taker.MarketID {
		return ErrMakerMarket
	}
	if maker.Side != opposite(taker.Side) {
		return ErrMakerSide
	}
	return nil
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

func priceCrosses(taker, maker domain.Order) bool {
	switch taker.Side {
	case domain.SideBuy:
		return maker.Price <= taker.Price
	case domain.SideSell:
		return maker.Price >= taker.Price
	default:
		return false
	}
}

func checkInvariant(original domain.Quantity, plan MatchPlan) error {
	var filled uint64
	for _, fill := range plan.Fills {
		if fill.Quantity == 0 || fill.Price == 0 {
			return ErrInvariant
		}
		var err error
		filled, err = arithmetic.Add(filled, uint64(fill.Quantity))
		if err != nil {
			return ErrInvariant
		}
	}
	total, err := arithmetic.Add(filled, uint64(plan.RemainingQuantity))
	if err != nil || total != uint64(original) {
		return ErrInvariant
	}
	return nil
}
