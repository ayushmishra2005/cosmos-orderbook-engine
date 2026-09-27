package keeper

import (
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func emit(ctx sdk.Context, typ string, attrs ...sdk.Attribute) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(typ, attrs...))
}

func u64(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func emitOrderEvents(ctx sdk.Context, plan executionPlan) {
	order := plan.taker.Order
	emit(ctx, types.EventTypeOrderAccepted,
		sdk.NewAttribute("order_id", order.ID.String()),
		sdk.NewAttribute("market_id", u64(uint64(order.MarketID))),
		sdk.NewAttribute("owner", sdk.AccAddress(plan.owner).String()),
		sdk.NewAttribute("remaining", u64(uint64(plan.remaining))),
		sdk.NewAttribute("rested", strconv.FormatBool(plan.rest)),
	)
	if len(plan.trades) > 0 {
		kind := types.EventTypeOrderPartiallyFilled
		if plan.remaining == 0 {
			kind = types.EventTypeOrderFilled
		}
		emit(ctx, kind,
			sdk.NewAttribute("order_id", order.ID.String()),
			sdk.NewAttribute("remaining", u64(uint64(plan.remaining))),
		)
	}
	for _, maker := range plan.makers {
		kind := types.EventTypeOrderPartiallyFilled
		if maker.remove {
			kind = types.EventTypeOrderFilled
		}
		emit(ctx, kind,
			sdk.NewAttribute("order_id", maker.order.Order.ID.String()),
			sdk.NewAttribute("remaining", u64(uint64(maker.order.Order.RemainingQuantity))),
		)
	}
	for _, trade := range plan.trades {
		emit(ctx, types.EventTypeTrade,
			sdk.NewAttribute("market_id", u64(uint64(trade.MarketID))),
			sdk.NewAttribute("sequence", u64(trade.Sequence)),
			sdk.NewAttribute("price", u64(uint64(trade.Price))),
			sdk.NewAttribute("quantity", u64(uint64(trade.Quantity))),
			sdk.NewAttribute("maker_fee", u64(trade.MakerFee)),
			sdk.NewAttribute("taker_fee", u64(trade.TakerFee)),
		)
	}
}

func addrString(owner []byte) string {
	if len(owner) == 0 {
		return ""
	}
	return sdk.AccAddress(owner).String()
}
