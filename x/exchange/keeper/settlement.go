package keeper

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/matching"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// executionPlan is the complete set of writes for one accepted command.
// It is applied only after every amount has been checked.
type executionPlan struct {
	taker            types.StoredOrder
	rest             bool
	makers           []makerChange
	balances         []balanceWrite
	trades           []types.Trade
	orderSequence    uint64
	setOrderSequence bool
	tradeSequence    uint64
	setTradeSequence bool
	owner            []byte
	nonce            uint64
	clientOrderID    []byte
	revision         uint64
	remaining        domain.Quantity
	stop             uint8
}

type makerChange struct {
	order  types.StoredOrder
	remove bool
}

type balanceWrite struct {
	owner []byte
	asset domain.AssetID
	bal   types.Balance
}

// incrementalFee is C(prev+gross) - C(prev), with C(g) = ceil(g * ppm / 1_000_000).
// Splitting a gross across fills does not change the sum of these fees.
func incrementalFee(prevGross, gross, ppm uint64) (uint64, uint64, error) {
	next, err := arithmetic.Add(prevGross, gross)
	if err != nil {
		return 0, 0, err
	}
	total, err := arithmetic.Fee(next, ppm, types.FeeDenominator)
	if err != nil {
		return 0, 0, err
	}
	already, err := arithmetic.Fee(prevGross, ppm, types.FeeDenominator)
	if err != nil {
		return 0, 0, err
	}
	fee, err := arithmetic.Sub(total, already)
	if err != nil {
		return 0, 0, err
	}
	return fee, next, nil
}

type balSet struct {
	k     Keeper
	ctx   sdk.Context
	items []balanceWrite
}

func (s *balSet) touch(owner []byte, asset domain.AssetID) (int, error) {
	for i := range s.items {
		if s.items[i].asset == asset && bytes.Equal(s.items[i].owner, owner) {
			return i, nil
		}
	}
	bal, err := s.k.GetBalance(s.ctx, owner, asset)
	if err != nil {
		return 0, err
	}
	s.items = append(s.items, balanceWrite{
		owner: append([]byte(nil), owner...),
		asset: asset,
		bal:   bal,
	})
	return len(s.items) - 1, nil
}

func (s *balSet) add(i int, locked bool, amount uint64) error {
	if amount == 0 {
		return nil
	}
	var err error
	if locked {
		s.items[i].bal.Locked, err = arithmetic.Add(s.items[i].bal.Locked, amount)
	} else {
		s.items[i].bal.Available, err = arithmetic.Add(s.items[i].bal.Available, amount)
	}
	return err
}

func (s *balSet) sub(i int, locked bool, amount uint64, under error) error {
	if amount == 0 {
		return nil
	}
	var next uint64
	var err error
	if locked {
		next, err = arithmetic.Sub(s.items[i].bal.Locked, amount)
	} else {
		next, err = arithmetic.Sub(s.items[i].bal.Available, amount)
	}
	if errors.Is(err, arithmetic.ErrUnderflow) {
		return under
	}
	if err != nil {
		return err
	}
	if locked {
		s.items[i].bal.Locked = next
	} else {
		s.items[i].bal.Available = next
	}
	return nil
}

func (k Keeper) settle(ctx sdk.Context, market types.Market, taker domain.Order, plan matching.MatchPlan) (executionPlan, error) {
	startQuote, err := k.GetBalance(ctx, taker.Owner, market.QuoteAssetID)
	if err != nil {
		return executionPlan{}, err
	}
	startBase, err := k.GetBalance(ctx, taker.Owner, market.BaseAssetID)
	if err != nil {
		return executionPlan{}, err
	}

	bals := &balSet{k: k, ctx: ctx}
	quoteIdx, err := bals.touch(taker.Owner, market.QuoteAssetID)
	if err != nil {
		return executionPlan{}, err
	}
	baseIdx, err := bals.touch(taker.Owner, market.BaseAssetID)
	if err != nil {
		return executionPlan{}, err
	}

	reserveAsset, reserveAmt, err := reserveOf(market, taker)
	if err != nil {
		return executionPlan{}, err
	}
	reserveIdx := quoteIdx
	if reserveAsset == market.BaseAssetID {
		reserveIdx = baseIdx
	}
	if err := bals.sub(reserveIdx, false, reserveAmt, types.ErrInsufficientBalance); err != nil {
		return executionPlan{}, err
	}
	if err := bals.add(reserveIdx, true, reserveAmt); err != nil {
		return executionPlan{}, err
	}

	stored := types.StoredOrder{Order: taker}
	makers := make([]types.StoredOrder, 0, len(plan.Fills))
	trades := make([]types.Trade, 0, len(plan.Fills))
	tradeSeq, err := k.GetTradeSequence(ctx, market.ID)
	if err != nil {
		return executionPlan{}, err
	}

	for _, fill := range plan.Fills {
		if fill.TakerOrderID != taker.ID || fill.Quantity == 0 || fill.Price == 0 {
			return executionPlan{}, types.ErrSettlement
		}
		mi := -1
		for i := range makers {
			if makers[i].Order.ID == fill.MakerOrderID {
				mi = i
				break
			}
		}
		if mi < 0 {
			loaded, err := k.GetOrder(ctx, fill.MakerOrderID)
			if err != nil {
				if errors.Is(err, types.ErrNotFound) {
					return executionPlan{}, types.ErrCorrupt
				}
				return executionPlan{}, err
			}
			makers = append(makers, loaded)
			mi = len(makers) - 1
		}
		maker := makers[mi]
		if maker.Order.Price != fill.Price || maker.Order.RemainingQuantity < fill.Quantity {
			return executionPlan{}, types.ErrSettlement
		}
		baseAmt, err := arithmetic.BaseAmount(fill.Quantity, market.BaseLotSize)
		if err != nil {
			return executionPlan{}, err
		}
		quoteAmt, err := arithmetic.Notional(fill.Quantity, fill.Price, market.QuoteAtomsPerTickPerLot)
		if err != nil {
			return executionPlan{}, err
		}

		takerIsBuyer := stored.Order.Side == domain.SideBuy
		var buyer, seller types.StoredOrder
		var buyerGross, sellerGross *uint64
		var buyerPPM, sellerPPM uint64
		if takerIsBuyer {
			buyer, seller = stored, maker
			buyerGross, sellerGross = &stored.TakerGross, &maker.MakerGross
			buyerPPM, sellerPPM = market.TakerFeePPM, market.MakerFeePPM
		} else {
			buyer, seller = maker, stored
			buyerGross, sellerGross = &maker.MakerGross, &stored.TakerGross
			buyerPPM, sellerPPM = market.MakerFeePPM, market.TakerFeePPM
		}

		// Locked quote is released at the buyer's own tick. The quote that
		// actually moves is the maker tick. The difference is the buyer's.
		reservedQuote, err := arithmetic.Notional(fill.Quantity, buyer.Order.Price, market.QuoteAtomsPerTickPerLot)
		if err != nil {
			return executionPlan{}, err
		}
		improvement, err := arithmetic.Sub(reservedQuote, quoteAmt)
		if err != nil {
			return executionPlan{}, err
		}
		buyerQuote, err := bals.touch(buyer.Order.Owner, market.QuoteAssetID)
		if err != nil {
			return executionPlan{}, err
		}
		buyerBase, err := bals.touch(buyer.Order.Owner, market.BaseAssetID)
		if err != nil {
			return executionPlan{}, err
		}
		sellerQuote, err := bals.touch(seller.Order.Owner, market.QuoteAssetID)
		if err != nil {
			return executionPlan{}, err
		}
		sellerBase, err := bals.touch(seller.Order.Owner, market.BaseAssetID)
		if err != nil {
			return executionPlan{}, err
		}
		if err := bals.sub(buyerQuote, true, reservedQuote, types.ErrSettlement); err != nil {
			return executionPlan{}, err
		}
		if err := bals.add(buyerQuote, false, improvement); err != nil {
			return executionPlan{}, err
		}
		if err := bals.sub(sellerBase, true, baseAmt, types.ErrSettlement); err != nil {
			return executionPlan{}, err
		}

		buyerFee, nextBuyerGross, err := incrementalFee(*buyerGross, baseAmt, buyerPPM)
		if err != nil {
			return executionPlan{}, err
		}
		*buyerGross = nextBuyerGross
		sellerFee, nextSellerGross, err := incrementalFee(*sellerGross, quoteAmt, sellerPPM)
		if err != nil {
			return executionPlan{}, err
		}
		*sellerGross = nextSellerGross

		baseToBuyer, err := arithmetic.Sub(baseAmt, buyerFee)
		if err != nil {
			return executionPlan{}, err
		}
		quoteToSeller, err := arithmetic.Sub(quoteAmt, sellerFee)
		if err != nil {
			return executionPlan{}, err
		}
		if err := bals.add(buyerBase, false, baseToBuyer); err != nil {
			return executionPlan{}, err
		}
		if err := bals.add(sellerQuote, false, quoteToSeller); err != nil {
			return executionPlan{}, err
		}
		if buyerFee > 0 {
			idx, err := bals.touch(types.FeeCollectorOwner, market.BaseAssetID)
			if err != nil {
				return executionPlan{}, err
			}
			if err := bals.add(idx, false, buyerFee); err != nil {
				return executionPlan{}, err
			}
		}
		if sellerFee > 0 {
			idx, err := bals.touch(types.FeeCollectorOwner, market.QuoteAssetID)
			if err != nil {
				return executionPlan{}, err
			}
			if err := bals.add(idx, false, sellerFee); err != nil {
				return executionPlan{}, err
			}
		}

		nextRemaining, err := arithmetic.Sub(uint64(maker.Order.RemainingQuantity), uint64(fill.Quantity))
		if err != nil {
			return executionPlan{}, err
		}
		maker.Order.RemainingQuantity = domain.Quantity(nextRemaining)
		makers[mi] = maker

		var makerFee, takerFee uint64
		if takerIsBuyer {
			takerFee, makerFee = buyerFee, sellerFee
		} else {
			makerFee, takerFee = buyerFee, sellerFee
		}
		nextTrade, err := domain.NextSequence(domain.Sequence(tradeSeq))
		if err != nil {
			return executionPlan{}, err
		}
		tradeSeq = uint64(nextTrade)
		trades = append(trades, types.Trade{
			MarketID:     market.ID,
			Sequence:     tradeSeq,
			MakerOrderID: maker.Order.ID,
			TakerOrderID: taker.ID,
			Price:        fill.Price,
			Quantity:     fill.Quantity,
			BaseAmount:   baseAmt,
			QuoteAmount:  quoteAmt,
			MakerFee:     makerFee,
			TakerFee:     takerFee,
			Buyer:        append([]byte(nil), buyer.Order.Owner...),
			Seller:       append([]byte(nil), seller.Order.Owner...),
		})
	}

	left := uint64(taker.OriginalQuantity)
	for _, fill := range plan.Fills {
		left, err = arithmetic.Sub(left, uint64(fill.Quantity))
		if err != nil {
			return executionPlan{}, err
		}
	}
	if domain.Quantity(left) != plan.RemainingQuantity {
		return executionPlan{}, types.ErrSettlement
	}
	stored.Order.RemainingQuantity = plan.RemainingQuantity

	lockedRemaining := plan.RemainingQuantity
	if !plan.RestIncoming {
		lockedRemaining = 0
		if plan.RemainingQuantity > 0 {
			releaseOrder := taker
			releaseOrder.RemainingQuantity = plan.RemainingQuantity
			_, releaseAmt, err := reserveOf(market, releaseOrder)
			if err != nil {
				return executionPlan{}, err
			}
			if err := bals.sub(reserveIdx, true, releaseAmt, types.ErrSettlement); err != nil {
				return executionPlan{}, err
			}
			if err := bals.add(reserveIdx, false, releaseAmt); err != nil {
				return executionPlan{}, err
			}
		}
	}

	if err := checkTakerLock(market, taker, startQuote, startBase, bals, quoteIdx, baseIdx, lockedRemaining); err != nil {
		return executionPlan{}, err
	}

	out := executionPlan{
		taker:     stored,
		rest:      plan.RestIncoming,
		balances:  bals.items,
		trades:    trades,
		owner:     append([]byte(nil), taker.Owner...),
		nonce:     taker.CommandNonce,
		remaining: plan.RemainingQuantity,
		stop:      uint8(plan.StopReason),
	}
	if len(trades) > 0 {
		out.setTradeSequence = true
		out.tradeSequence = tradeSeq
	}
	for _, maker := range makers {
		change := makerChange{order: maker}
		if maker.Order.RemainingQuantity == 0 {
			change.remove = true
		} else if err := maker.Order.ValidateResting(); err != nil {
			return executionPlan{}, err
		}
		out.makers = append(out.makers, change)
	}
	if plan.RestIncoming {
		last, err := k.GetOrderSequence(ctx, market.ID)
		if err != nil {
			return executionPlan{}, err
		}
		next, err := domain.NextSequence(domain.Sequence(last))
		if err != nil {
			return executionPlan{}, err
		}
		out.taker.Order.Sequence = next
		out.taker.Order.RemainingQuantity = plan.RemainingQuantity
		if err := out.taker.Order.ValidateResting(); err != nil {
			return executionPlan{}, err
		}
		out.setOrderSequence = true
		out.orderSequence = uint64(next)
	}

	rev, err := k.GetRevision(ctx)
	if err != nil {
		return executionPlan{}, err
	}
	out.revision, err = arithmetic.Add(rev, 1)
	if err != nil {
		return executionPlan{}, err
	}
	return out, nil
}

func checkTakerLock(market types.Market, taker domain.Order, startQuote, startBase types.Balance, bals *balSet, quoteIdx, baseIdx int, lockedRemaining domain.Quantity) error {
	endQuote := bals.items[quoteIdx].bal
	endBase := bals.items[baseIdx].bal
	if taker.Side == domain.SideBuy {
		want, err := arithmetic.Notional(lockedRemaining, taker.Price, market.QuoteAtomsPerTickPerLot)
		if err != nil {
			return err
		}
		sum, err := arithmetic.Add(startQuote.Locked, want)
		if err != nil {
			return err
		}
		if endQuote.Locked != sum || endBase.Locked != startBase.Locked {
			return types.ErrSettlement
		}
		return nil
	}
	want, err := arithmetic.BaseAmount(lockedRemaining, market.BaseLotSize)
	if err != nil {
		return err
	}
	sum, err := arithmetic.Add(startBase.Locked, want)
	if err != nil {
		return err
	}
	if endBase.Locked != sum || endQuote.Locked != startQuote.Locked {
		return types.ErrSettlement
	}
	return nil
}
