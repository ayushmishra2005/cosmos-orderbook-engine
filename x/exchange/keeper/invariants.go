package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func corrupt(reason string) error {
	return fmt.Errorf("%w: %s", types.ErrCorrupt, reason)
}

// CheckInvariants reads exchange state and returns an error when a stored
// relationship does not hold. It does not write and it does not repair.
func (k Keeper) CheckInvariants(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height, err := executionHeight(sdkCtx)
	if err != nil {
		return err
	}
	markets, err := k.marketsByID(ctx)
	if err != nil {
		return err
	}
	orders, byID, err := k.activeOrders(ctx)
	if err != nil {
		return err
	}
	if err := k.checkOrders(orders, markets); err != nil {
		return err
	}
	bookCount, err := k.checkBookIndexes(ctx, byID)
	if err != nil {
		return err
	}
	ownerCount, err := k.checkOwnerIndexes(ctx, byID)
	if err != nil {
		return err
	}
	clientCount, err := k.checkClientIndexes(ctx, byID)
	if err != nil {
		return err
	}
	expCount, err := k.checkExpirationIndexes(ctx, byID)
	if err != nil {
		return err
	}
	for _, order := range orders {
		id := order.Order.ID
		if bookCount[id] != 1 {
			return corrupt("active order book index count")
		}
		if ownerCount[id] != 1 {
			return corrupt("owner open-order index count")
		}
		if len(order.ClientOrderID) == 0 {
			if clientCount[id] != 0 {
				return corrupt("client index on an order without a client id")
			}
		} else if clientCount[id] != 1 {
			return corrupt("client index count")
		}
		if order.Order.TimeInForce == domain.TimeInForceGTD {
			if expCount[id] != 1 {
				return corrupt("GTD order expiration index")
			}
		} else if expCount[id] != 0 {
			return corrupt("expiration index on a non-GTD order")
		}
	}
	if err := k.checkReservations(ctx, orders, markets); err != nil {
		return err
	}
	if err := k.checkSequences(ctx, orders, markets); err != nil {
		return err
	}
	if err := k.checkNonces(ctx, orders); err != nil {
		return err
	}
	if err := k.checkTrades(ctx, markets); err != nil {
		return err
	}
	return k.checkNotCrossed(ctx, markets, height)
}

// CheckCustody checks the internal liability identity and, when a bank keeper
// is set, that module custody covers it.
//
// Fee-collector balances are excluded from the other owners' sums and added
// once. Bank custody may exceed that liability: genesis accepts a module
// balance greater than the internal sum.
func (k Keeper) CheckCustody(ctx context.Context) error {
	var available, locked, collector uint64
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixBalance}, func(key, value []byte) (bool, error) {
		owner, _, err := canonical.DecodeBalanceKey(key)
		if err != nil {
			return false, corrupt("balance key")
		}
		bal, err := types.DecodeBalance(value)
		if err != nil {
			return false, err
		}
		if err := requireNonNegative(bal); err != nil {
			return false, err
		}
		if bytes.Equal(owner, types.FeeCollectorOwner) {
			sum, err := arithmetic.Add(bal.Available, bal.Locked)
			if err != nil {
				return false, err
			}
			collector, err = arithmetic.Add(collector, sum)
			return false, err
		}
		available, err = arithmetic.Add(available, bal.Available)
		if err != nil {
			return false, err
		}
		locked, err = arithmetic.Add(locked, bal.Locked)
		return false, err
	})
	if err != nil {
		return err
	}
	internal, err := arithmetic.Add(available, locked)
	if err != nil {
		return err
	}
	liability, err := arithmetic.Add(internal, collector)
	if err != nil {
		return err
	}
	again, err := arithmetic.Add(available, locked)
	if err != nil {
		return err
	}
	again, err = arithmetic.Add(again, collector)
	if err != nil || again != liability {
		return corrupt("liability identity")
	}
	if k.bank == nil {
		return nil
	}
	var summed uint64
	var assets int
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixAsset}, func(_, value []byte) (bool, error) {
		asset, err := types.DecodeAsset(value)
		if err != nil {
			return false, err
		}
		assets++
		sum, err := k.SumLiabilities(ctx, asset.ID)
		if err != nil {
			return false, err
		}
		summed, err = arithmetic.Add(summed, sum)
		if err != nil {
			return false, err
		}
		coin := k.bank.GetBalance(ctx, ModuleAddress(), asset.Denom)
		if !coin.Amount.IsUint64() || coin.Amount.LT(sdkmath.NewIntFromUint64(sum)) {
			return false, types.ErrUnbacked
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	if assets > 0 && summed != liability {
		return corrupt("liability does not match per-asset sums")
	}
	return nil
}

func requireNonNegative(bal types.Balance) error {
	// Available and locked are uint64. A decoded balance cannot be negative.
	// A short or trailing encoding is rejected by DecodeBalance.
	return nil
}

func (k Keeper) marketsByID(ctx context.Context) ([]types.Market, error) {
	var markets []types.Market
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixMarket}, func(key, value []byte) (bool, error) {
		id, err := canonical.DecodeMarketKey(key)
		if err != nil {
			return false, corrupt("market key")
		}
		market, err := types.DecodeMarket(value)
		if err != nil {
			return false, err
		}
		if market.ID != id {
			return false, corrupt("market id")
		}
		if err := market.Validate(); err != nil {
			return false, corrupt("market")
		}
		markets = append(markets, market)
		return false, nil
	})
	return markets, err
}

func (k Keeper) activeOrders(ctx context.Context) ([]types.StoredOrder, map[domain.OrderID]types.StoredOrder, error) {
	var orders []types.StoredOrder
	byID := make(map[domain.OrderID]types.StoredOrder)
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixActiveOrder}, func(key, value []byte) (bool, error) {
		id, err := canonical.DecodeActiveOrderKey(key)
		if err != nil {
			return false, corrupt("active order key")
		}
		order, err := types.DecodeOrder(id, value)
		if err != nil {
			return false, corrupt("active order")
		}
		if _, ok := byID[id]; ok {
			return false, corrupt("duplicate active order")
		}
		byID[id] = order
		orders = append(orders, order)
		return false, nil
	})
	return orders, byID, err
}

func (k Keeper) checkOrders(orders []types.StoredOrder, markets []types.Market) error {
	byMarket := make(map[domain.MarketID]types.Market, len(markets))
	for _, market := range markets {
		byMarket[market.ID] = market
	}
	type seqKey struct {
		market   domain.MarketID
		sequence domain.Sequence
	}
	seenSeq := make(map[seqKey]struct{}, len(orders))
	for _, stored := range orders {
		order := stored.Order
		if order.RemainingQuantity > order.OriginalQuantity {
			return corrupt("remaining exceeds original")
		}
		if order.RemainingQuantity == 0 {
			return corrupt("active order has no remaining quantity")
		}
		if order.Type == domain.OrderTypeMarket {
			return corrupt("market order is resting")
		}
		if order.TimeInForce == domain.TimeInForceIOC || order.TimeInForce == domain.TimeInForceFOK {
			return corrupt("immediate order is resting")
		}
		if order.TimeInForce != domain.TimeInForceGTC && order.TimeInForce != domain.TimeInForceGTD {
			return corrupt("resting time in force")
		}
		if _, ok := byMarket[order.MarketID]; !ok {
			return corrupt("order market")
		}
		key := seqKey{order.MarketID, order.Sequence}
		if _, ok := seenSeq[key]; ok {
			return corrupt("duplicate order sequence")
		}
		seenSeq[key] = struct{}{}
		if bytes.Equal(order.Owner, types.FeeCollectorOwner) {
			return corrupt("fee collector order")
		}
	}
	return nil
}

func (k Keeper) checkBookIndexes(ctx context.Context, byID map[domain.OrderID]types.StoredOrder) (map[domain.OrderID]int, error) {
	count := make(map[domain.OrderID]int)
	for _, side := range []struct {
		prefix byte
		decode func([]byte) (domain.MarketID, domain.Price, domain.Sequence, error)
		want   domain.Side
	}{
		{canonical.PrefixAskBook, canonical.DecodeAskKey, domain.SideSell},
		{canonical.PrefixBidBook, canonical.DecodeBidKey, domain.SideBuy},
	} {
		err := k.iteratePrefix(ctx, []byte{side.prefix}, func(key, value []byte) (bool, error) {
			if len(value) != len(domain.OrderID{}) {
				return false, corrupt("book index value")
			}
			var id domain.OrderID
			copy(id[:], value)
			marketID, price, sequence, err := side.decode(key)
			if err != nil {
				return false, corrupt("book index key")
			}
			order, ok := byID[id]
			if !ok {
				return false, corrupt("book index points to missing order")
			}
			got := order.Order
			if got.Side != side.want {
				return false, corrupt("book side")
			}
			if got.MarketID != marketID {
				return false, corrupt("book market")
			}
			if got.Price != price {
				return false, corrupt("book price")
			}
			if got.Sequence != sequence {
				return false, corrupt("book sequence")
			}
			if got.ID != id {
				return false, corrupt("book order id")
			}
			count[id]++
			return false, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return count, nil
}

func (k Keeper) checkOwnerIndexes(ctx context.Context, byID map[domain.OrderID]types.StoredOrder) (map[domain.OrderID]int, error) {
	count := make(map[domain.OrderID]int)
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixOwnerOpenOrder}, func(key, value []byte) (bool, error) {
		owner, marketID, id, err := canonical.DecodeOwnerOpenOrderKey(key)
		if err != nil {
			return false, corrupt("owner index key")
		}
		if len(value) != len(id) || !bytes.Equal(value, id[:]) {
			return false, corrupt("owner index value")
		}
		order, ok := byID[id]
		if !ok {
			return false, corrupt("owner index points to missing order")
		}
		if !bytes.Equal(order.Order.Owner, owner) || order.Order.MarketID != marketID {
			return false, corrupt("owner index")
		}
		count[id]++
		return false, nil
	})
	return count, err
}

func (k Keeper) checkClientIndexes(ctx context.Context, byID map[domain.OrderID]types.StoredOrder) (map[domain.OrderID]int, error) {
	count := make(map[domain.OrderID]int)
	type clientKey struct {
		owner  string
		client string
	}
	seen := make(map[clientKey]domain.OrderID)
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixActiveClientOrder}, func(key, value []byte) (bool, error) {
		owner, clientID, err := canonical.DecodeActiveClientOrderKey(key)
		if err != nil {
			return false, corrupt("client index key")
		}
		if len(value) != len(domain.OrderID{}) {
			return false, corrupt("client index value")
		}
		var id domain.OrderID
		copy(id[:], value)
		ck := clientKey{string(owner), string(clientID)}
		if prev, ok := seen[ck]; ok && prev != id {
			return false, corrupt("duplicate active client id")
		}
		seen[ck] = id
		order, ok := byID[id]
		if !ok {
			return false, corrupt("client index points to missing order")
		}
		if !bytes.Equal(order.Order.Owner, owner) || !bytes.Equal(order.ClientOrderID, clientID) {
			return false, corrupt("client index owner")
		}
		count[id]++
		return false, nil
	})
	return count, err
}

func (k Keeper) checkExpirationIndexes(ctx context.Context, byID map[domain.OrderID]types.StoredOrder) (map[domain.OrderID]int, error) {
	count := make(map[domain.OrderID]int)
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixExpiration}, func(key, value []byte) (bool, error) {
		height, id, err := canonical.DecodeExpirationKey(key)
		if err != nil {
			return false, corrupt("expiration key")
		}
		if len(value) != len(id) || !bytes.Equal(value, id[:]) {
			return false, corrupt("expiration value")
		}
		order, ok := byID[id]
		if !ok {
			return false, corrupt("expiration index points to missing order")
		}
		if order.Order.TimeInForce != domain.TimeInForceGTD || order.Order.ExpiryHeight != height {
			return false, corrupt("expiration index")
		}
		count[id]++
		return false, nil
	})
	return count, err
}

type ownerAsset struct {
	owner string
	asset domain.AssetID
}

func (k Keeper) checkReservations(ctx context.Context, orders []types.StoredOrder, markets []types.Market) error {
	byMarket := make(map[domain.MarketID]types.Market, len(markets))
	for _, market := range markets {
		byMarket[market.ID] = market
	}
	expected := make(map[ownerAsset]uint64)
	var keys []ownerAsset
	for _, stored := range orders {
		market, ok := byMarket[stored.Order.MarketID]
		if !ok {
			return corrupt("reservation market")
		}
		asset, amount, err := reserveOf(market, stored.Order)
		if err != nil {
			return err
		}
		if amount == 0 {
			return corrupt("zero reservation")
		}
		key := ownerAsset{string(stored.Order.Owner), asset}
		if _, ok := expected[key]; !ok {
			keys = append(keys, key)
		}
		expected[key], err = arithmetic.Add(expected[key], amount)
		if err != nil {
			return err
		}
	}
	seen := make(map[ownerAsset]struct{}, len(keys))
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixBalance}, func(key, value []byte) (bool, error) {
		owner, asset, err := canonical.DecodeBalanceKey(key)
		if err != nil {
			return false, corrupt("balance key")
		}
		bal, err := types.DecodeBalance(value)
		if err != nil {
			return false, err
		}
		if err := requireNonNegative(bal); err != nil {
			return false, err
		}
		oa := ownerAsset{string(owner), asset}
		if bal.Locked != expected[oa] {
			return false, corrupt("locked balance does not match reservations")
		}
		seen[oa] = struct{}{}
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, ok := seen[key]; !ok {
			return corrupt("reservation without a balance")
		}
	}
	return nil
}

func (k Keeper) checkSequences(ctx context.Context, orders []types.StoredOrder, markets []types.Market) error {
	maxSeq := make(map[domain.MarketID]uint64)
	for _, order := range orders {
		seq := uint64(order.Order.Sequence)
		if seq > maxSeq[order.Order.MarketID] {
			maxSeq[order.Order.MarketID] = seq
		}
	}
	for _, market := range markets {
		got, err := k.GetOrderSequence(ctx, market.ID)
		if err != nil {
			return err
		}
		if got < maxSeq[market.ID] {
			return corrupt("order sequence moved backward")
		}
	}
	return nil
}

func (k Keeper) checkNonces(ctx context.Context, orders []types.StoredOrder) error {
	nonces := make(map[string]uint64)
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixAccountNonce}, func(key, value []byte) (bool, error) {
		owner, err := canonical.DecodeAccountNonceKey(key)
		if err != nil {
			return false, corrupt("nonce key")
		}
		nonce, err := types.DecodeUint64(value)
		if err != nil {
			return false, err
		}
		if nonce == 0 {
			return false, corrupt("stored nonce is zero")
		}
		nonces[string(owner)] = nonce
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, order := range orders {
		if nonces[string(order.Order.Owner)] < order.Order.CommandNonce {
			return corrupt("command nonce moved backward")
		}
	}
	return nil
}

func (k Keeper) checkTrades(ctx context.Context, markets []types.Market) error {
	byMarket := make(map[domain.MarketID]types.Market, len(markets))
	for _, market := range markets {
		byMarket[market.ID] = market
	}
	last := make(map[domain.MarketID]uint64)
	var current domain.MarketID
	var expect uint64
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixTrade}, func(key, value []byte) (bool, error) {
		marketID, sequence, err := canonical.DecodeTradeKey(key)
		if err != nil {
			return false, corrupt("trade key")
		}
		market, ok := byMarket[marketID]
		if !ok {
			return false, corrupt("trade market")
		}
		trade, err := types.DecodeTrade(value)
		if err != nil {
			return false, err
		}
		if trade.MarketID != marketID || trade.Sequence != sequence {
			return false, corrupt("trade record")
		}
		if err := fillConserves(market, trade); err != nil {
			return false, err
		}
		if marketID != current {
			current = marketID
			expect = 1
		}
		if sequence != expect {
			return false, corrupt("trade sequence gap")
		}
		expect++
		last[marketID] = sequence
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, market := range markets {
		got, err := k.GetTradeSequence(ctx, market.ID)
		if err != nil {
			return err
		}
		if got != last[market.ID] {
			return corrupt("trade sequence moved backward")
		}
	}
	return nil
}

func (k Keeper) checkNotCrossed(ctx context.Context, markets []types.Market, height uint64) error {
	for _, market := range markets {
		ask, askOK, err := k.bestExecutable(ctx, market.ID, domain.SideSell, height)
		if err != nil {
			return err
		}
		bid, bidOK, err := k.bestExecutable(ctx, market.ID, domain.SideBuy, height)
		if err != nil {
			return err
		}
		if askOK && bidOK && uint64(bid.Price) >= uint64(ask.Price) {
			return corrupt("resting book is crossed")
		}
	}
	return nil
}

func (k Keeper) bestExecutable(ctx context.Context, marketID domain.MarketID, side domain.Side, height uint64) (domain.Order, bool, error) {
	src, err := k.openBook(ctx, marketID, side)
	if err != nil {
		return domain.Order{}, false, err
	}
	defer src.Close()
	for {
		order, ok, err := src.Peek()
		if err != nil || !ok {
			return domain.Order{}, false, err
		}
		if !order.ExpiredAt(height) {
			return order, true, nil
		}
		if err := src.Next(); err != nil {
			return domain.Order{}, false, err
		}
	}
}

// FillConserves checks one fill against the market.
// Buyer fees are base atoms. Seller fees are quote atoms.
// Base removed from the seller equals base received by the buyer plus the buyer fee.
// Quote paid by the buyer equals quote received by the seller plus the seller fee.
// takerIsBuyer selects which stored fee is the buyer fee. The formula is the
// existing incremental fee; this does not introduce another one.
func FillConserves(market types.Market, takerIsBuyer bool, trade types.Trade) error {
	base, err := arithmetic.BaseAmount(trade.Quantity, market.BaseLotSize)
	if err != nil {
		return err
	}
	quote, err := arithmetic.Notional(trade.Quantity, trade.Price, market.QuoteAtomsPerTickPerLot)
	if err != nil {
		return err
	}
	if trade.BaseAmount != base || trade.QuoteAmount != quote || trade.Price == 0 || trade.Quantity == 0 {
		return corrupt("trade amounts")
	}
	buyerFee, sellerFee := trade.TakerFee, trade.MakerFee
	if !takerIsBuyer {
		buyerFee, sellerFee = trade.MakerFee, trade.TakerFee
	}
	if buyerFee > base || sellerFee > quote {
		return corrupt("fee exceeds received asset")
	}
	baseToBuyer, err := arithmetic.Sub(base, buyerFee)
	if err != nil {
		return err
	}
	quoteToSeller, err := arithmetic.Sub(quote, sellerFee)
	if err != nil {
		return err
	}
	gotBase, err := arithmetic.Add(baseToBuyer, buyerFee)
	if err != nil || gotBase != base {
		return corrupt("base conservation")
	}
	gotQuote, err := arithmetic.Add(quoteToSeller, sellerFee)
	if err != nil || gotQuote != quote {
		return corrupt("quote conservation")
	}
	return nil
}

// fillConserves accepts the assignment of maker and taker fees that matches
// the stored grosses. Historical trades do not keep the taker side.
func fillConserves(market types.Market, trade types.Trade) error {
	if err := FillConserves(market, true, trade); err == nil {
		return nil
	}
	return FillConserves(market, false, trade)
}
