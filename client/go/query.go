package orderbook

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func (c *Client) exchange() exchangev1.QueryClient {
	return exchangev1.NewQueryClient(c.grpcConn)
}

func (c *Client) batch() batchv1.QueryClient {
	return batchv1.NewQueryClient(c.grpcConn)
}

// GetMarket returns one market.
func (c *Client) GetMarket(ctx context.Context, marketID uint64) (Market, error) {
	if marketID == 0 {
		return Market{}, fmt.Errorf("%w: market id", ErrInvalidArgument)
	}
	res, err := c.exchange().Market(ctx, &exchangev1.QueryMarketRequest{MarketId: marketID})
	if err != nil {
		return Market{}, mapQuery(err)
	}
	if res.Market == nil {
		return Market{}, ErrNotFound
	}
	return marketFrom(res.Market), nil
}

// GetMarkets returns one page of markets.
func (c *Client) GetMarkets(ctx context.Context, limit uint32, offset uint64) (MarketsPage, error) {
	res, err := c.exchange().Markets(ctx, &exchangev1.QueryMarketsRequest{Limit: limit, Offset: offset})
	if err != nil {
		return MarketsPage{}, mapQuery(err)
	}
	out := MarketsPage{NextOffset: res.NextOffset, Markets: make([]Market, 0, len(res.Markets))}
	for _, m := range res.Markets {
		if m != nil {
			out.Markets = append(out.Markets, marketFrom(m))
		}
	}
	return out, nil
}

// GetBalance returns one owner's balance for an asset.
func (c *Client) GetBalance(ctx context.Context, owner string, assetID uint64) (Balance, error) {
	if owner == "" || assetID == 0 {
		return Balance{}, fmt.Errorf("%w: owner and asset", ErrInvalidArgument)
	}
	res, err := c.exchange().Balance(ctx, &exchangev1.QueryBalanceRequest{Owner: owner, AssetId: assetID})
	if err != nil {
		return Balance{}, mapQuery(err)
	}
	if res.Balance == nil {
		return Balance{}, ErrNotFound
	}
	return balanceFrom(res.Balance), nil
}

// GetBalances returns one page of an owner's balances.
func (c *Client) GetBalances(ctx context.Context, owner string, limit uint32, offset uint64) (BalancesPage, error) {
	if owner == "" {
		return BalancesPage{}, fmt.Errorf("%w: owner", ErrInvalidArgument)
	}
	res, err := c.exchange().Balances(ctx, &exchangev1.QueryBalancesRequest{Owner: owner, Limit: limit, Offset: offset})
	if err != nil {
		return BalancesPage{}, mapQuery(err)
	}
	out := BalancesPage{NextOffset: res.NextOffset, Balances: make([]Balance, 0, len(res.Balances))}
	for _, b := range res.Balances {
		if b != nil {
			out.Balances = append(out.Balances, balanceFrom(b))
		}
	}
	return out, nil
}

// GetOrder returns one order by the chain-assigned ID.
func (c *Client) GetOrder(ctx context.Context, id domain.OrderID) (Order, error) {
	if id.IsZero() {
		return Order{}, fmt.Errorf("%w: order id", ErrInvalidArgument)
	}
	res, err := c.exchange().Order(ctx, &exchangev1.QueryOrderRequest{OrderId: id[:]})
	if err != nil {
		return Order{}, mapQuery(err)
	}
	if res.Order == nil {
		return Order{}, ErrNotFound
	}
	return orderFrom(res.Order)
}

// GetOpenOrders returns one page of an owner's resting orders.
// marketID 0 lists every market, matching the query service.
func (c *Client) GetOpenOrders(ctx context.Context, owner string, marketID uint64, limit uint32, offset uint64) (OrdersPage, error) {
	if owner == "" {
		return OrdersPage{}, fmt.Errorf("%w: owner", ErrInvalidArgument)
	}
	res, err := c.exchange().OpenOrders(ctx, &exchangev1.QueryOpenOrdersRequest{
		Owner: owner, MarketId: marketID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return OrdersPage{}, mapQuery(err)
	}
	out := OrdersPage{NextOffset: res.NextOffset, Orders: make([]Order, 0, len(res.Orders))}
	for _, o := range res.Orders {
		if o == nil {
			continue
		}
		parsed, err := orderFrom(o)
		if err != nil {
			return OrdersPage{}, err
		}
		out.Orders = append(out.Orders, parsed)
	}
	return out, nil
}

// GetOrderbook returns asks and bids in canonical book order.
func (c *Client) GetOrderbook(ctx context.Context, marketID uint64, depth uint32) (Orderbook, error) {
	if marketID == 0 {
		return Orderbook{}, fmt.Errorf("%w: market id", ErrInvalidArgument)
	}
	res, err := c.exchange().Orderbook(ctx, &exchangev1.QueryOrderbookRequest{MarketId: marketID, Depth: depth})
	if err != nil {
		return Orderbook{}, mapQuery(err)
	}
	var book Orderbook
	var errParse error
	book.Asks, errParse = bookSide(res.Asks)
	if errParse != nil {
		return Orderbook{}, errParse
	}
	book.Bids, errParse = bookSide(res.Bids)
	if errParse != nil {
		return Orderbook{}, errParse
	}
	return book, nil
}

// GetTrades returns trades with sequence greater than afterSequence.
func (c *Client) GetTrades(ctx context.Context, marketID, afterSequence uint64, limit uint32) (TradesPage, error) {
	if marketID == 0 {
		return TradesPage{}, fmt.Errorf("%w: market id", ErrInvalidArgument)
	}
	res, err := c.exchange().Trades(ctx, &exchangev1.QueryTradesRequest{
		MarketId: marketID, AfterSequence: afterSequence, Limit: limit,
	})
	if err != nil {
		return TradesPage{}, mapQuery(err)
	}
	out := TradesPage{NextSequence: res.NextSequence, Trades: make([]Trade, 0, len(res.Trades))}
	for _, tr := range res.Trades {
		if tr == nil {
			continue
		}
		parsed, err := tradeFrom(tr)
		if err != nil {
			return TradesPage{}, err
		}
		out.Trades = append(out.Trades, parsed)
	}
	return out, nil
}

// GetExchangeRevision returns the current exchange revision.
func (c *Client) GetExchangeRevision(ctx context.Context) (uint64, error) {
	res, err := c.exchange().ExchangeRevision(ctx, &exchangev1.QueryExchangeRevisionRequest{})
	if err != nil {
		return 0, mapQuery(err)
	}
	return res.Revision, nil
}

// GetBatch returns one finalized batch by number.
func (c *Client) GetBatch(ctx context.Context, number uint64) (Batch, error) {
	if number == 0 {
		return Batch{}, fmt.Errorf("%w: batch number", ErrInvalidArgument)
	}
	res, err := c.batch().Batch(ctx, &batchv1.QueryBatchRequest{BatchNumber: number})
	if err != nil {
		return Batch{}, mapQuery(err)
	}
	return batchFrom(res.Batch)
}

// GetLatestBatch returns the highest finalized batch.
func (c *Client) GetLatestBatch(ctx context.Context) (Batch, error) {
	res, err := c.batch().LatestBatch(ctx, &batchv1.QueryLatestBatchRequest{})
	if err != nil {
		return Batch{}, mapQuery(err)
	}
	return batchFrom(res.Batch)
}

// GetBatchByID returns the batch with that BatchID.
func (c *Client) GetBatchByID(ctx context.Context, id [32]byte) (Batch, error) {
	var zero [32]byte
	if id == zero {
		return Batch{}, fmt.Errorf("%w: batch id", ErrInvalidArgument)
	}
	res, err := c.batch().BatchByID(ctx, &batchv1.QueryBatchByIDRequest{BatchId: id[:]})
	if err != nil {
		return Batch{}, mapQuery(err)
	}
	return batchFrom(res.Batch)
}

// GetBatchCommitment returns the commitment fields for one batch number.
func (c *Client) GetBatchCommitment(ctx context.Context, number uint64) (Commitment, error) {
	if number == 0 {
		return Commitment{}, fmt.Errorf("%w: batch number", ErrInvalidArgument)
	}
	res, err := c.batch().BatchCommitment(ctx, &batchv1.QueryBatchCommitmentRequest{BatchNumber: number})
	if err != nil {
		return Commitment{}, mapQuery(err)
	}
	out := Commitment{Number: res.BatchNumber}
	if err := copy32(&out.BatchID, res.BatchId); err != nil {
		return Commitment{}, err
	}
	if err := copy32(&out.Previous, res.PreviousBatchCommitment); err != nil {
		return Commitment{}, err
	}
	if err := copy32(&out.Commitment, res.BatchCommitment); err != nil {
		return Commitment{}, err
	}
	if err := copy32(&out.ResultsHash, res.ResultsHash); err != nil {
		return Commitment{}, err
	}
	return out, nil
}

func domainSide(v exchangev1.Side) (domain.Side, error) {
	switch v {
	case exchangev1.Side_SIDE_BUY:
		return domain.SideBuy, nil
	case exchangev1.Side_SIDE_SELL:
		return domain.SideSell, nil
	default:
		return 0, fmt.Errorf("%w: side", ErrInvalidArgument)
	}
}

func domainOrderType(v exchangev1.OrderType) (domain.OrderType, error) {
	switch v {
	case exchangev1.OrderType_ORDER_TYPE_LIMIT:
		return domain.OrderTypeLimit, nil
	case exchangev1.OrderType_ORDER_TYPE_MARKET:
		return domain.OrderTypeMarket, nil
	default:
		return 0, fmt.Errorf("%w: order type", ErrInvalidArgument)
	}
}

func domainTimeInForce(v exchangev1.TimeInForce) (domain.TimeInForce, error) {
	switch v {
	case exchangev1.TimeInForce_TIME_IN_FORCE_GTC:
		return domain.TimeInForceGTC, nil
	case exchangev1.TimeInForce_TIME_IN_FORCE_IOC:
		return domain.TimeInForceIOC, nil
	case exchangev1.TimeInForce_TIME_IN_FORCE_FOK:
		return domain.TimeInForceFOK, nil
	case exchangev1.TimeInForce_TIME_IN_FORCE_GTD:
		return domain.TimeInForceGTD, nil
	default:
		return 0, fmt.Errorf("%w: time in force", ErrInvalidArgument)
	}
}

func marketFrom(m *exchangev1.Market) Market {
	return Market{
		ID: m.Id, BaseAssetID: m.BaseAssetId, QuoteAssetID: m.QuoteAssetId,
		BaseDenom: m.BaseDenom, QuoteDenom: m.QuoteDenom,
		BaseLotSize: m.BaseLotSize, QuoteAtomsPerTickPerLot: m.QuoteAtomsPerTickPerLot,
		MakerFeePPM: m.MakerFeePpm, TakerFeePPM: m.TakerFeePpm,
		MaxMakerVisits: m.MaxMakerVisits, Enabled: m.Enabled,
	}
}

func balanceFrom(b *exchangev1.Balance) Balance {
	return Balance{Owner: b.Owner, AssetID: b.AssetId, Denom: b.Denom, Available: b.Available, Locked: b.Locked}
}

func orderFrom(o *exchangev1.Order) (Order, error) {
	id, err := orderID(o.OrderId)
	if err != nil {
		return Order{}, err
	}
	side, err := domainSide(o.Side)
	if err != nil {
		return Order{}, err
	}
	orderType, err := domainOrderType(o.OrderType)
	if err != nil {
		return Order{}, err
	}
	tif, err := domainTimeInForce(o.TimeInForce)
	if err != nil {
		return Order{}, err
	}
	return Order{
		ID: id, Owner: o.Owner, MarketID: o.MarketId,
		Side: side, Type: orderType, TimeInForce: tif,
		PriceTicks: o.PriceTicks, OriginalLots: o.OriginalLots, RemainingLots: o.RemainingLots,
		Sequence: o.Sequence, ExpiryHeight: o.ExpiryHeight, CommandNonce: o.CommandNonce,
	}, nil
}

func bookSide(in []*exchangev1.BookOrder) ([]BookOrder, error) {
	out := make([]BookOrder, 0, len(in))
	for _, b := range in {
		if b == nil {
			continue
		}
		id, err := orderID(b.OrderId)
		if err != nil {
			return nil, err
		}
		side, err := domainSide(b.Side)
		if err != nil {
			return nil, err
		}
		out = append(out, BookOrder{
			ID: id, Side: side, PriceTicks: b.PriceTicks,
			RemainingLots: b.RemainingLots, Sequence: b.Sequence,
		})
	}
	return out, nil
}

func tradeFrom(tr *exchangev1.Trade) (Trade, error) {
	maker, err := orderID(tr.MakerOrderId)
	if err != nil {
		return Trade{}, err
	}
	taker, err := orderID(tr.TakerOrderId)
	if err != nil {
		return Trade{}, err
	}
	return Trade{
		MarketID: tr.MarketId, Sequence: tr.Sequence, MakerOrderID: maker, TakerOrderID: taker,
		PriceTicks: tr.PriceTicks, QuantityLots: tr.QuantityLots,
		BaseAmount: tr.BaseAmount, QuoteAmount: tr.QuoteAmount,
		MakerFee: tr.MakerFee, TakerFee: tr.TakerFee, Buyer: tr.Buyer, Seller: tr.Seller,
	}, nil
}

func batchFrom(b *batchv1.Batch) (Batch, error) {
	if b == nil {
		return Batch{}, ErrNotFound
	}
	out := Batch{
		Number: b.BatchNumber, Height: b.ExecutionHeight,
		PreRevision: b.PreExchangeRevision, PostRevision: b.PostExchangeRevision,
	}
	if err := copy32(&out.ID, b.BatchId); err != nil {
		return Batch{}, err
	}
	if err := copy32(&out.Previous, b.PreviousBatchCommitment); err != nil {
		return Batch{}, err
	}
	if err := copy32(&out.Commitment, b.BatchCommitment); err != nil {
		return Batch{}, err
	}
	if err := copy32(&out.ResultsHash, b.ResultsHash); err != nil {
		return Batch{}, err
	}
	out.Results = make([]BatchResult, 0, len(b.Results))
	for _, r := range b.Results {
		if r == nil {
			continue
		}
		id, err := orderID(r.OrderId)
		if err != nil {
			return Batch{}, err
		}
		item := BatchResult{
			Index: r.Index, Command: commandName(r.CommandType), Owner: r.Owner,
			OrderID: id, Status: statusName(r.Status), RemainingLots: r.RemainingLots,
		}
		for _, tr := range r.Trades {
			if tr == nil {
				continue
			}
			item.Trades = append(item.Trades, TradeRef{MarketID: tr.MarketId, Sequence: tr.Sequence})
		}
		out.Results = append(out.Results, item)
	}
	return out, nil
}

func commandName(t batchv1.CommandType) string {
	switch t {
	case batchv1.CommandType_COMMAND_TYPE_PLACE_ORDER:
		return "place"
	case batchv1.CommandType_COMMAND_TYPE_CANCEL_ORDER:
		return "cancel"
	default:
		return ""
	}
}

func statusName(s batchv1.OrderStatus) string {
	switch s {
	case batchv1.OrderStatus_ORDER_STATUS_RESTING:
		return "resting"
	case batchv1.OrderStatus_ORDER_STATUS_FILLED:
		return "filled"
	case batchv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "cancelled"
	case batchv1.OrderStatus_ORDER_STATUS_UNFILLED:
		return "unfilled"
	default:
		return ""
	}
}

func orderID(bz []byte) (domain.OrderID, error) {
	if len(bz) != len(domain.OrderID{}) {
		return domain.OrderID{}, fmt.Errorf("%w: order id length", ErrInvalidArgument)
	}
	var id domain.OrderID
	copy(id[:], bz)
	if id.IsZero() {
		return domain.OrderID{}, fmt.Errorf("%w: order id", ErrInvalidArgument)
	}
	return id, nil
}

func copy32(dst *[32]byte, src []byte) error {
	if len(src) != 32 {
		return fmt.Errorf("%w: expected 32 bytes", ErrInvalidArgument)
	}
	copy(dst[:], src)
	return nil
}

func mapQuery(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
		}
		return err
	}
	msg := st.Message()
	switch st.Code() {
	case codes.NotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", ErrInvalidArgument, msg)
	case codes.Unavailable:
		return fmt.Errorf("%w: %s", ErrGRPCUnavailable, msg)
	default:
		if strings.Contains(strings.ToLower(msg), "not found") {
			return fmt.Errorf("%w: %s", ErrNotFound, msg)
		}
		return fmt.Errorf("%s", msg)
	}
}
