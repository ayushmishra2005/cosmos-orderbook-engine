package v1

import (
	"bytes"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// GenesisActiveOrder is one resting order carried in genesis.
// ClientOrderID is empty when the order has no active client id.
type GenesisActiveOrder struct {
	Order         exchangetypes.StoredOrder
	ClientOrderID []byte
}

// ActiveOrders decodes resting orders. Indexes are not part of genesis.
func (gs GenesisState) ActiveOrders() ([]GenesisActiveOrder, error) {
	return gs.activeOrders()
}

// StoredTrades decodes the trade history. The sequence counter is separate.
func (gs GenesisState) StoredTrades() ([]exchangetypes.Trade, error) {
	markets := make(map[domain.MarketID]exchangetypes.Market, len(gs.Markets))
	for _, market := range gs.Markets {
		if market == nil {
			return nil, fmt.Errorf("nil market")
		}
		parsed := exchangetypes.Market{
			ID:                      domain.MarketID(market.Id),
			BaseAssetID:             domain.AssetID(market.BaseAssetId),
			QuoteAssetID:            domain.AssetID(market.QuoteAssetId),
			BaseLotSize:             market.BaseLotSize,
			QuoteAtomsPerTickPerLot: market.QuoteAtomsPerTickPerLot,
			MakerFeePPM:             market.MakerFeePpm,
			TakerFeePPM:             market.TakerFeePpm,
			MaxMakerVisits:          market.MaxMakerVisits,
			Enabled:                 market.Enabled,
		}
		if err := parsed.Validate(); err != nil {
			return nil, err
		}
		markets[parsed.ID] = parsed
	}
	return gs.storedTrades(markets)
}

// ValidateOrderIDs checks each order ID against the chain and instance.
// Call it before writing genesis. A mismatch is rejected.
func (gs GenesisState) ValidateOrderIDs(chainID string) error {
	orders, err := gs.activeOrders()
	if err != nil {
		return err
	}
	for _, order := range orders {
		id, err := canonical.HashOrderID(canonical.OrderIDInput{
			ChainID:            chainID,
			ExchangeInstanceID: []byte(gs.InstanceId),
			Owner:              order.Order.Order.Owner,
			MarketID:           order.Order.Order.MarketID,
			CommandNonce:       order.Order.Order.CommandNonce,
		})
		if err != nil {
			return err
		}
		if id != order.Order.Order.ID {
			return domain.ErrInvalidOrderID
		}
	}
	return nil
}

type ownerAsset struct {
	owner string
	asset domain.AssetID
}

func (gs GenesisState) activeOrders() ([]GenesisActiveOrder, error) {
	out := make([]GenesisActiveOrder, 0, len(gs.Orders))
	seenID := make(map[domain.OrderID]struct{}, len(gs.Orders))
	seenSeq := make(map[[2]uint64]struct{}, len(gs.Orders))
	seenBook := make(map[string]struct{}, len(gs.Orders))
	seenClient := make(map[clientKey]struct{}, len(gs.Orders))
	for _, pb := range gs.Orders {
		order, err := decodeGenesisOrder(pb)
		if err != nil {
			return nil, err
		}
		id := order.Order.Order.ID
		if _, ok := seenID[id]; ok {
			return nil, exchangetypes.ErrExists
		}
		seenID[id] = struct{}{}
		seqKey := [2]uint64{uint64(order.Order.Order.MarketID), uint64(order.Order.Order.Sequence)}
		if _, ok := seenSeq[seqKey]; ok {
			return nil, exchangetypes.ErrExists
		}
		seenSeq[seqKey] = struct{}{}
		book, err := genesisBookKey(order.Order.Order)
		if err != nil {
			return nil, err
		}
		if _, ok := seenBook[string(book)]; ok {
			return nil, exchangetypes.ErrExists
		}
		seenBook[string(book)] = struct{}{}
		if len(order.ClientOrderID) > 0 {
			ck := clientKey{string(order.Order.Order.Owner), string(order.ClientOrderID)}
			if _, ok := seenClient[ck]; ok {
				return nil, exchangetypes.ErrExists
			}
			seenClient[ck] = struct{}{}
		}
		out = append(out, order)
	}
	return out, nil
}

type clientKey struct {
	owner  string
	client string
}

func decodeGenesisOrder(pb *GenesisOrder) (GenesisActiveOrder, error) {
	if pb == nil {
		return GenesisActiveOrder{}, fmt.Errorf("nil order")
	}
	if len(pb.OrderId) != len(domain.OrderID{}) {
		return GenesisActiveOrder{}, domain.ErrInvalidOrderID
	}
	var id domain.OrderID
	copy(id[:], pb.OrderId)
	owner, err := sdk.AccAddressFromBech32(pb.Owner)
	if err != nil {
		return GenesisActiveOrder{}, err
	}
	if exchangetypes.IsFeeCollector(owner) {
		return GenesisActiveOrder{}, exchangetypes.ErrFeeCollector
	}
	if pb.CommandNonce == 0 {
		return GenesisActiveOrder{}, fmt.Errorf("%w: command nonce", exchangetypes.ErrCorrupt)
	}
	stored := exchangetypes.StoredOrder{
		Order: domain.Order{
			ID:                id,
			Owner:             append([]byte(nil), owner...),
			MarketID:          domain.MarketID(pb.MarketId),
			Side:              domain.Side(pb.Side),
			Type:              domain.OrderType(pb.OrderType),
			TimeInForce:       domain.TimeInForce(pb.TimeInForce),
			Price:             domain.Price(pb.PriceTicks),
			OriginalQuantity:  domain.Quantity(pb.OriginalLots),
			RemainingQuantity: domain.Quantity(pb.RemainingLots),
			Sequence:          domain.Sequence(pb.Sequence),
			ExpiryHeight:      pb.ExpiryHeight,
			CommandNonce:      pb.CommandNonce,
		},
		TakerGross: pb.TakerGross,
		MakerGross: pb.MakerGross,
	}
	if err := stored.Order.ValidateResting(); err != nil {
		return GenesisActiveOrder{}, err
	}
	var client []byte
	if len(pb.ClientOrderId) > 0 {
		if _, err := canonical.EncodeActiveClientOrderKey(owner, pb.ClientOrderId); err != nil {
			return GenesisActiveOrder{}, err
		}
		client = append([]byte(nil), pb.ClientOrderId...)
	}
	return GenesisActiveOrder{Order: stored, ClientOrderID: client}, nil
}

func genesisBookKey(order domain.Order) ([]byte, error) {
	switch order.Side {
	case domain.SideBuy:
		return canonical.EncodeBidKey(order.MarketID, order.Price, order.Sequence)
	case domain.SideSell:
		return canonical.EncodeAskKey(order.MarketID, order.Price, order.Sequence)
	default:
		return nil, domain.ErrInvalidSide
	}
}

func (gs GenesisState) storedTrades(markets map[domain.MarketID]exchangetypes.Market) ([]exchangetypes.Trade, error) {
	out := make([]exchangetypes.Trade, 0, len(gs.Trades))
	seen := make(map[[2]uint64]struct{}, len(gs.Trades))
	for _, pb := range gs.Trades {
		if pb == nil {
			return nil, fmt.Errorf("nil trade")
		}
		market, ok := markets[domain.MarketID(pb.MarketId)]
		if !ok {
			return nil, domain.ErrInvalidMarket
		}
		trade, err := decodeGenesisTrade(pb)
		if err != nil {
			return nil, err
		}
		key := [2]uint64{uint64(trade.MarketID), trade.Sequence}
		if _, ok := seen[key]; ok {
			return nil, exchangetypes.ErrExists
		}
		seen[key] = struct{}{}
		if trade.MakerOrderID == trade.TakerOrderID {
			return nil, fmt.Errorf("%w: trade order", exchangetypes.ErrCorrupt)
		}
		if err := tradeConserves(market, trade); err != nil {
			return nil, err
		}
		out = append(out, trade)
	}
	return out, nil
}

func decodeGenesisTrade(pb *Trade) (exchangetypes.Trade, error) {
	if len(pb.MakerOrderId) != len(domain.OrderID{}) || len(pb.TakerOrderId) != len(domain.OrderID{}) {
		return exchangetypes.Trade{}, domain.ErrInvalidOrderID
	}
	var makerID, takerID domain.OrderID
	copy(makerID[:], pb.MakerOrderId)
	copy(takerID[:], pb.TakerOrderId)
	if makerID.IsZero() || takerID.IsZero() {
		return exchangetypes.Trade{}, domain.ErrInvalidOrderID
	}
	if pb.Sequence == 0 {
		return exchangetypes.Trade{}, domain.ErrInvalidSequence
	}
	buyer, err := sdk.AccAddressFromBech32(pb.Buyer)
	if err != nil {
		return exchangetypes.Trade{}, err
	}
	seller, err := sdk.AccAddressFromBech32(pb.Seller)
	if err != nil {
		return exchangetypes.Trade{}, err
	}
	return exchangetypes.Trade{
		MarketID:     domain.MarketID(pb.MarketId),
		Sequence:     pb.Sequence,
		MakerOrderID: makerID,
		TakerOrderID: takerID,
		Price:        domain.Price(pb.PriceTicks),
		Quantity:     domain.Quantity(pb.QuantityLots),
		BaseAmount:   pb.BaseAmount,
		QuoteAmount:  pb.QuoteAmount,
		MakerFee:     pb.MakerFee,
		TakerFee:     pb.TakerFee,
		Buyer:        append([]byte(nil), buyer...),
		Seller:       append([]byte(nil), seller...),
	}, nil
}

// tradeConserves matches the keeper fill check. Historical trades do not
// record which side was the taker, so either fee assignment may fit.
func tradeConserves(market exchangetypes.Market, trade exchangetypes.Trade) error {
	if err := tradeConservesSide(market, trade, true); err == nil {
		return nil
	}
	return tradeConservesSide(market, trade, false)
}

func tradeConservesSide(market exchangetypes.Market, trade exchangetypes.Trade, takerIsBuyer bool) error {
	base, err := arithmetic.BaseAmount(trade.Quantity, market.BaseLotSize)
	if err != nil {
		return err
	}
	quote, err := arithmetic.Notional(trade.Quantity, trade.Price, market.QuoteAtomsPerTickPerLot)
	if err != nil {
		return err
	}
	if trade.BaseAmount != base || trade.QuoteAmount != quote || trade.Price == 0 || trade.Quantity == 0 {
		return fmt.Errorf("%w: trade amounts", exchangetypes.ErrCorrupt)
	}
	buyerFee, sellerFee := trade.TakerFee, trade.MakerFee
	if !takerIsBuyer {
		buyerFee, sellerFee = trade.MakerFee, trade.TakerFee
	}
	if buyerFee > base || sellerFee > quote {
		return fmt.Errorf("%w: fee exceeds received asset", exchangetypes.ErrCorrupt)
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
		return fmt.Errorf("%w: base conservation", exchangetypes.ErrCorrupt)
	}
	gotQuote, err := arithmetic.Add(quoteToSeller, sellerFee)
	if err != nil || gotQuote != quote {
		return fmt.Errorf("%w: quote conservation", exchangetypes.ErrCorrupt)
	}
	return nil
}

func (gs GenesisState) validateBalances(assets map[uint64]string, orders []GenesisActiveOrder, markets map[domain.MarketID]exchangetypes.Market) error {
	required := make(map[ownerAsset]uint64)
	for _, order := range orders {
		market, ok := markets[order.Order.Order.MarketID]
		if !ok {
			return domain.ErrInvalidMarket
		}
		asset, amount, err := orderReserve(market, order.Order.Order)
		if err != nil {
			return err
		}
		if amount == 0 {
			return fmt.Errorf("%w: zero reserve", exchangetypes.ErrCorrupt)
		}
		key := ownerAsset{string(order.Order.Order.Owner), asset}
		required[key], err = arithmetic.Add(required[key], amount)
		if err != nil {
			return err
		}
	}
	seen := make(map[ownerAsset]struct{}, len(gs.Balances))
	for _, bal := range gs.Balances {
		if bal == nil {
			return fmt.Errorf("nil balance")
		}
		if _, ok := assets[bal.AssetId]; !ok {
			return domain.ErrInvalidAsset
		}
		owner, err := sdk.AccAddressFromBech32(bal.Owner)
		if err != nil {
			return err
		}
		if err := exchangetypes.ValidateBalanceCapacity(bal.Available, bal.Locked); err != nil {
			return err
		}
		if bal.Available == 0 && bal.Locked == 0 {
			return exchangetypes.ErrInvalidAmount
		}
		key := ownerAsset{string(owner), domain.AssetID(bal.AssetId)}
		if _, ok := seen[key]; ok {
			return exchangetypes.ErrExists
		}
		seen[key] = struct{}{}
		if bal.Locked != required[key] {
			if bal.Locked < required[key] {
				return fmt.Errorf("%w: locked balance does not cover orders", exchangetypes.ErrInsufficientBalance)
			}
			return fmt.Errorf("%w: locked balance is not backed by an order", exchangetypes.ErrCorrupt)
		}
	}
	for _, order := range orders {
		market := markets[order.Order.Order.MarketID]
		asset, _, err := orderReserve(market, order.Order.Order)
		if err != nil {
			return err
		}
		key := ownerAsset{string(order.Order.Order.Owner), asset}
		if _, ok := seen[key]; !ok {
			return fmt.Errorf("%w: order reserve has no locked balance", exchangetypes.ErrInsufficientBalance)
		}
	}
	return nil
}

func orderReserve(market exchangetypes.Market, order domain.Order) (domain.AssetID, uint64, error) {
	switch order.Side {
	case domain.SideBuy:
		amount, err := arithmetic.Notional(order.RemainingQuantity, order.Price, market.QuoteAtomsPerTickPerLot)
		return market.QuoteAssetID, amount, err
	case domain.SideSell:
		amount, err := arithmetic.BaseAmount(order.RemainingQuantity, market.BaseLotSize)
		return market.BaseAssetID, amount, err
	default:
		return 0, 0, domain.ErrInvalidSide
	}
}

func (gs GenesisState) validateNonces(orders []GenesisActiveOrder) error {
	nonces := make(map[string]uint64, len(gs.Nonces))
	for _, nonce := range gs.Nonces {
		if nonce == nil || nonce.Nonce == 0 {
			return fmt.Errorf("%w: command nonce", exchangetypes.ErrCorrupt)
		}
		owner, err := sdk.AccAddressFromBech32(nonce.Owner)
		if err != nil {
			return err
		}
		if _, ok := nonces[string(owner)]; ok {
			return exchangetypes.ErrExists
		}
		nonces[string(owner)] = nonce.Nonce
	}
	for _, order := range orders {
		owner := string(order.Order.Order.Owner)
		if nonces[owner] < order.Order.Order.CommandNonce {
			return fmt.Errorf("%w: command nonce behind order", exchangetypes.ErrCorrupt)
		}
	}
	return nil
}

func (gs GenesisState) validateSequences(markets map[domain.MarketID]exchangetypes.Market, orders []GenesisActiveOrder, trades []exchangetypes.Trade) error {
	orderSeq := make(map[domain.MarketID]uint64, len(gs.OrderSequences))
	for _, seq := range gs.OrderSequences {
		if seq == nil || seq.Sequence == 0 {
			return domain.ErrInvalidSequence
		}
		id := domain.MarketID(seq.MarketId)
		if _, ok := markets[id]; !ok {
			return domain.ErrInvalidMarket
		}
		if _, ok := orderSeq[id]; ok {
			return exchangetypes.ErrExists
		}
		orderSeq[id] = seq.Sequence
	}
	for _, order := range orders {
		if orderSeq[order.Order.Order.MarketID] < uint64(order.Order.Order.Sequence) {
			return fmt.Errorf("%w: order sequence behind", exchangetypes.ErrCorrupt)
		}
	}
	tradeSeq := make(map[domain.MarketID]uint64, len(gs.TradeSequences))
	for _, seq := range gs.TradeSequences {
		if seq == nil || seq.Sequence == 0 {
			return domain.ErrInvalidSequence
		}
		id := domain.MarketID(seq.MarketId)
		if _, ok := markets[id]; !ok {
			return domain.ErrInvalidMarket
		}
		if _, ok := tradeSeq[id]; ok {
			return exchangetypes.ErrExists
		}
		tradeSeq[id] = seq.Sequence
	}
	byMarket := make(map[domain.MarketID][]exchangetypes.Trade)
	var marketIDs []domain.MarketID
	for _, trade := range trades {
		if _, ok := byMarket[trade.MarketID]; !ok {
			marketIDs = append(marketIDs, trade.MarketID)
		}
		byMarket[trade.MarketID] = append(byMarket[trade.MarketID], trade)
	}
	sort.Slice(marketIDs, func(i, j int) bool { return marketIDs[i] < marketIDs[j] })
	for _, id := range marketIDs {
		list := append([]exchangetypes.Trade(nil), byMarket[id]...)
		sort.Slice(list, func(i, j int) bool { return list[i].Sequence < list[j].Sequence })
		for i, trade := range list {
			if trade.Sequence != uint64(i)+1 {
				return fmt.Errorf("%w: trade sequence gap", exchangetypes.ErrCorrupt)
			}
		}
		if tradeSeq[id] != uint64(len(list)) {
			return fmt.Errorf("%w: trade sequence", exchangetypes.ErrCorrupt)
		}
	}
	for id, seq := range tradeSeq {
		if _, ok := byMarket[id]; !ok && seq != 0 {
			return fmt.Errorf("%w: trade sequence", exchangetypes.ErrCorrupt)
		}
	}
	return nil
}

type grossAcc struct {
	taker uint64
	maker uint64
	lots  uint64
}

func validateFeeGross(orders []GenesisActiveOrder, trades []exchangetypes.Trade) error {
	byID := make(map[domain.OrderID]exchangetypes.StoredOrder, len(orders))
	for _, order := range orders {
		byID[order.Order.Order.ID] = order.Order
	}
	got := make(map[domain.OrderID]grossAcc, len(orders))
	for _, trade := range trades {
		if err := attributeGross(trade, true, byID, got); err != nil {
			return err
		}
		if err := attributeGross(trade, false, byID, got); err != nil {
			return err
		}
	}
	for _, order := range orders {
		filled, err := arithmetic.Sub(uint64(order.Order.Order.OriginalQuantity), uint64(order.Order.Order.RemainingQuantity))
		if err != nil {
			return err
		}
		acc := got[order.Order.Order.ID]
		if acc.lots != filled || acc.taker != order.Order.TakerGross || acc.maker != order.Order.MakerGross {
			return fmt.Errorf("%w: fee gross", exchangetypes.ErrCorrupt)
		}
	}
	return nil
}

func attributeGross(trade exchangetypes.Trade, maker bool, byID map[domain.OrderID]exchangetypes.StoredOrder, got map[domain.OrderID]grossAcc) error {
	id := trade.TakerOrderID
	if maker {
		id = trade.MakerOrderID
	}
	stored, ok := byID[id]
	if !ok {
		return nil
	}
	order := stored.Order
	if maker && trade.Price != order.Price {
		return fmt.Errorf("%w: trade price", exchangetypes.ErrCorrupt)
	}
	if !maker {
		if order.Side == domain.SideBuy && trade.Price > order.Price {
			return fmt.Errorf("%w: trade price", exchangetypes.ErrCorrupt)
		}
		if order.Side == domain.SideSell && trade.Price < order.Price {
			return fmt.Errorf("%w: trade price", exchangetypes.ErrCorrupt)
		}
	}
	party := trade.Seller
	gross := trade.QuoteAmount
	if order.Side == domain.SideBuy {
		party = trade.Buyer
		gross = trade.BaseAmount
	}
	if !bytes.Equal(party, order.Owner) {
		return fmt.Errorf("%w: trade party", exchangetypes.ErrCorrupt)
	}
	acc := got[id]
	var err error
	if maker {
		acc.maker, err = arithmetic.Add(acc.maker, gross)
	} else {
		acc.taker, err = arithmetic.Add(acc.taker, gross)
	}
	if err != nil {
		return err
	}
	acc.lots, err = arithmetic.Add(acc.lots, uint64(trade.Quantity))
	if err != nil {
		return err
	}
	got[id] = acc
	return nil
}
