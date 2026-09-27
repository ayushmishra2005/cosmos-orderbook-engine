package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

type queryServer struct {
	k Keeper
}

// NewQueryServer returns the exchange query server.
func NewQueryServer(k Keeper) v1.QueryServer {
	return queryServer{k: k}
}

func (q queryServer) Market(ctx context.Context, req *v1.QueryMarketRequest) (*v1.QueryMarketResponse, error) {
	market, err := q.k.GetMarket(ctx, domain.MarketID(req.MarketId))
	if err != nil {
		return nil, err
	}
	denoms, err := q.denoms(ctx, market.BaseAssetID, market.QuoteAssetID)
	if err != nil {
		return nil, err
	}
	return &v1.QueryMarketResponse{Market: marketToProto(market, denoms)}, nil
}

func (q queryServer) Markets(ctx context.Context, req *v1.QueryMarketsRequest) (*v1.QueryMarketsResponse, error) {
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	markets, next, err := q.k.listMarkets(ctx, req.Offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.Market, 0, len(markets))
	for _, market := range markets {
		denoms, err := q.denoms(ctx, market.BaseAssetID, market.QuoteAssetID)
		if err != nil {
			return nil, err
		}
		out = append(out, marketToProto(market, denoms))
	}
	return &v1.QueryMarketsResponse{Markets: out, NextOffset: next}, nil
}

func (q queryServer) Balance(ctx context.Context, req *v1.QueryBalanceRequest) (*v1.QueryBalanceResponse, error) {
	owner, err := sdk.AccAddressFromBech32(req.Owner)
	if err != nil {
		return nil, err
	}
	asset, err := q.k.GetAsset(ctx, domain.AssetID(req.AssetId))
	if err != nil {
		return nil, err
	}
	bal, err := q.k.GetBalance(ctx, owner, asset.ID)
	if err != nil {
		return nil, err
	}
	return &v1.QueryBalanceResponse{Balance: balanceProto(owner, asset, bal)}, nil
}

func (q queryServer) Balances(ctx context.Context, req *v1.QueryBalancesRequest) (*v1.QueryBalancesResponse, error) {
	owner, err := sdk.AccAddressFromBech32(req.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	bals, assets, next, err := q.k.listBalances(ctx, owner, req.Offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.Balance, 0, len(bals))
	for i, bal := range bals {
		asset, err := q.k.GetAsset(ctx, assets[i])
		if err != nil {
			return nil, err
		}
		out = append(out, balanceProto(owner, asset, bal))
	}
	return &v1.QueryBalancesResponse{Balances: out, NextOffset: next}, nil
}

func (q queryServer) Order(ctx context.Context, req *v1.QueryOrderRequest) (*v1.QueryOrderResponse, error) {
	id, err := orderIDFromBytes(req.OrderId)
	if err != nil {
		return nil, err
	}
	order, err := q.k.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	return &v1.QueryOrderResponse{Order: orderProto(order)}, nil
}

func (q queryServer) OpenOrders(ctx context.Context, req *v1.QueryOpenOrdersRequest) (*v1.QueryOpenOrdersResponse, error) {
	owner, err := sdk.AccAddressFromBech32(req.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	orders, next, err := q.k.listOpenOrders(ctx, owner, domain.MarketID(req.MarketId), req.Offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.Order, 0, len(orders))
	for _, order := range orders {
		out = append(out, orderProto(order))
	}
	return &v1.QueryOpenOrdersResponse{Orders: out, NextOffset: next}, nil
}

func (q queryServer) Orderbook(ctx context.Context, req *v1.QueryOrderbookRequest) (*v1.QueryOrderbookResponse, error) {
	depth, err := pageLimit(req.Depth)
	if err != nil {
		return nil, err
	}
	if _, err := q.k.GetMarket(ctx, domain.MarketID(req.MarketId)); err != nil {
		return nil, err
	}
	asks, err := q.k.listBook(ctx, domain.MarketID(req.MarketId), domain.SideSell, int(req.AskOffset), depth)
	if err != nil {
		return nil, err
	}
	bids, err := q.k.listBook(ctx, domain.MarketID(req.MarketId), domain.SideBuy, int(req.BidOffset), depth)
	if err != nil {
		return nil, err
	}
	return &v1.QueryOrderbookResponse{Asks: bookProto(asks), Bids: bookProto(bids)}, nil
}

func (q queryServer) Trades(ctx context.Context, req *v1.QueryTradesRequest) (*v1.QueryTradesResponse, error) {
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	if _, err := q.k.GetMarket(ctx, domain.MarketID(req.MarketId)); err != nil {
		return nil, err
	}
	trades, next, err := q.k.listTrades(ctx, domain.MarketID(req.MarketId), req.AfterSequence, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.Trade, 0, len(trades))
	for _, trade := range trades {
		out = append(out, tradeProto(trade))
	}
	return &v1.QueryTradesResponse{Trades: out, NextSequence: next}, nil
}

func (q queryServer) ExchangeRevision(ctx context.Context, _ *v1.QueryExchangeRevisionRequest) (*v1.QueryExchangeRevisionResponse, error) {
	rev, err := q.k.GetRevision(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.QueryExchangeRevisionResponse{Revision: rev}, nil
}

func (q queryServer) denoms(ctx context.Context, ids ...domain.AssetID) (map[domain.AssetID]string, error) {
	out := make(map[domain.AssetID]string, len(ids))
	for _, id := range ids {
		asset, err := q.k.GetAsset(ctx, id)
		if err != nil {
			return nil, err
		}
		out[id] = asset.Denom
	}
	return out, nil
}

func balanceProto(owner sdk.AccAddress, asset types.Asset, bal types.Balance) *v1.Balance {
	return &v1.Balance{
		Owner:     owner.String(),
		AssetId:   uint64(asset.ID),
		Denom:     asset.Denom,
		Available: bal.Available,
		Locked:    bal.Locked,
	}
}

func orderProto(order types.StoredOrder) *v1.Order {
	o := order.Order
	return &v1.Order{
		OrderId:       append([]byte(nil), o.ID[:]...),
		Owner:         addrString(o.Owner),
		MarketId:      uint64(o.MarketID),
		Side:          v1.Side(o.Side),
		OrderType:     v1.OrderType(o.Type),
		TimeInForce:   v1.TimeInForce(o.TimeInForce),
		PriceTicks:    uint64(o.Price),
		OriginalLots:  uint64(o.OriginalQuantity),
		RemainingLots: uint64(o.RemainingQuantity),
		Sequence:      uint64(o.Sequence),
		ExpiryHeight:  o.ExpiryHeight,
		CommandNonce:  o.CommandNonce,
	}
}

func bookProto(orders []types.StoredOrder) []*v1.BookOrder {
	out := make([]*v1.BookOrder, 0, len(orders))
	for _, order := range orders {
		o := order.Order
		out = append(out, &v1.BookOrder{
			OrderId:       append([]byte(nil), o.ID[:]...),
			Side:          v1.Side(o.Side),
			PriceTicks:    uint64(o.Price),
			RemainingLots: uint64(o.RemainingQuantity),
			Sequence:      uint64(o.Sequence),
		})
	}
	return out
}

func tradeProto(t types.Trade) *v1.Trade {
	return &v1.Trade{
		MarketId:     uint64(t.MarketID),
		Sequence:     t.Sequence,
		MakerOrderId: append([]byte(nil), t.MakerOrderID[:]...),
		TakerOrderId: append([]byte(nil), t.TakerOrderID[:]...),
		PriceTicks:   uint64(t.Price),
		QuantityLots: uint64(t.Quantity),
		BaseAmount:   t.BaseAmount,
		QuoteAmount:  t.QuoteAmount,
		MakerFee:     t.MakerFee,
		TakerFee:     t.TakerFee,
		Buyer:        addrString(t.Buyer),
		Seller:       addrString(t.Seller),
	}
}
