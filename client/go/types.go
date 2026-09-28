package orderbook

import "github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"

// Market is one trading pair. Prices are ticks. Quantities are lots.
type Market struct {
	ID                      uint64
	BaseAssetID             uint64
	QuoteAssetID            uint64
	BaseDenom               string
	QuoteDenom              string
	BaseLotSize             uint64
	QuoteAtomsPerTickPerLot uint64
	MakerFeePPM             uint64
	TakerFeePPM             uint64
	MaxMakerVisits          uint32
	Enabled                 bool
}

// MarketsPage is one page of markets.
type MarketsPage struct {
	Markets    []Market
	NextOffset uint64
}

// Balance is available and locked atoms for one asset.
type Balance struct {
	Owner     string
	AssetID   uint64
	Denom     string
	Available uint64
	Locked    uint64
}

// BalancesPage is one page of balances.
type BalancesPage struct {
	Balances   []Balance
	NextOffset uint64
}

// Order is a live order. The chain assigns OrderID.
type Order struct {
	ID            domain.OrderID
	Owner         string
	MarketID      uint64
	Side          domain.Side
	Type          domain.OrderType
	TimeInForce   domain.TimeInForce
	PriceTicks    uint64
	OriginalLots  uint64
	RemainingLots uint64
	Sequence      uint64
	ExpiryHeight  uint64
	CommandNonce  uint64
}

// OrdersPage is one page of open orders.
type OrdersPage struct {
	Orders     []Order
	NextOffset uint64
}

// BookOrder is one resting order in canonical book order.
type BookOrder struct {
	ID            domain.OrderID
	Side          domain.Side
	PriceTicks    uint64
	RemainingLots uint64
	Sequence      uint64
}

// Orderbook is a depth snapshot. Asks and bids stay in chain order.
type Orderbook struct {
	Asks []BookOrder
	Bids []BookOrder
}

// Trade is one persisted fill.
type Trade struct {
	MarketID     uint64
	Sequence     uint64
	MakerOrderID domain.OrderID
	TakerOrderID domain.OrderID
	PriceTicks   uint64
	QuantityLots uint64
	BaseAmount   uint64
	QuoteAmount  uint64
	MakerFee     uint64
	TakerFee     uint64
	Buyer        string
	Seller       string
}

// TradesPage is trades after a sequence, in chain order.
type TradesPage struct {
	Trades       []Trade
	NextSequence uint64
}

// BatchResult is one command outcome inside a finalized batch.
type BatchResult struct {
	Index         uint32
	Command       string
	Owner         string
	OrderID       domain.OrderID
	Status        string
	RemainingLots uint64
	Trades        []TradeRef
}

// TradeRef identifies one fill by market and trade sequence.
type TradeRef struct {
	MarketID uint64
	Sequence uint64
}

// Batch is one finalized batch record.
// Commitment is not an exchange state root.
type Batch struct {
	Number       uint64
	ID           [32]byte
	Height       uint64
	PreRevision  uint64
	PostRevision uint64
	Previous     [32]byte
	Commitment   [32]byte
	ResultsHash  [32]byte
	Results      []BatchResult
}

// Commitment is the chained digest of one finalized batch.
type Commitment struct {
	Number      uint64
	BatchID     [32]byte
	Previous    [32]byte
	Commitment  [32]byte
	ResultsHash [32]byte
}

// LimitOrder is a direct place-limit message.
// PriceTicks is the limit. The chain derives the order ID.
type LimitOrder struct {
	MarketID      uint64
	Side          domain.Side
	TimeInForce   domain.TimeInForce
	QuantityLots  uint64
	PriceTicks    uint64
	ExpiryHeight  uint64
	CommandNonce  uint64
	ClientOrderID []byte
}

// MarketOrder is a direct place-market message.
// WorstPriceTicks is the worst acceptable tick. It is required.
// The chain derives the order ID.
type MarketOrder struct {
	MarketID        uint64
	Side            domain.Side
	TimeInForce     domain.TimeInForce
	QuantityLots    uint64
	WorstPriceTicks uint64
	CommandNonce    uint64
	ClientOrderID   []byte
}

// Cancel is a direct cancel message.
type Cancel struct {
	OrderID      domain.OrderID
	CommandNonce uint64
}

// TxResult is a committed transaction.
// OrderID is set when the message response carries one.
type TxResult struct {
	Hash          string
	Height        int64
	Code          uint32
	Log           string
	OrderID       domain.OrderID
	RemainingLots uint64
	Rested        bool
	Released      uint64
	AssetID       uint64
}

// PlaceCommand is the body of an owner-signed place command.
// PriceTicks is the limit, or the worst acceptable tick for a market order.
type PlaceCommand struct {
	MarketID      uint64
	Side          domain.Side
	Type          domain.OrderType
	TimeInForce   domain.TimeInForce
	QuantityLots  uint64
	PriceTicks    uint64
	ExpiryHeight  uint64
	ClientOrderID []byte
}

// Admission is a sequencer response.
// Provisional means the command was journaled, not executed and not included.
type Admission struct {
	Accepted          bool
	SequencerPosition uint64
	Status            string
	Provisional       bool
}

// SequencerHealth is the sequencer process report. It is not chain state.
type SequencerHealth struct {
	Status              string
	Pending             int
	Inflight            int
	LatestObservedBatch uint64
	Chain               string
}

// Kind is a committed chain event type.
type Kind string

const (
	KindOrderAccepted        Kind = "order_accepted"
	KindOrderPartiallyFilled Kind = "order_partially_filled"
	KindOrderFilled          Kind = "order_filled"
	KindOrderCancelled       Kind = "order_cancelled"
	KindOrderExpired         Kind = "order_expired"
	KindTradeExecuted        Kind = "trade_executed"
	KindBatchFinalized       Kind = "batch_finalized"
)

// EventID identifies one emitted event.
// Delivery across reconnects is at-least-once. The same ID can be observed again.
type EventID struct {
	Height int64
	TxHash string
	Index  int
}

// Event is one committed exchange or batch event, in chain order.
type Event struct {
	ID    EventID
	Kind  Kind
	Order *OrderEvent
	Trade *TradeEvent
	Batch *BatchEvent
}

// OrderEvent carries the attributes the chain emitted for an order event.
type OrderEvent struct {
	OrderID   []byte
	MarketID  uint64
	Owner     string
	Remaining uint64
	Rested    bool
	AssetID   uint64
	Released  uint64
}

// TradeEvent carries the attributes the chain emitted for a trade.
type TradeEvent struct {
	MarketID     uint64
	Sequence     uint64
	PriceTicks   uint64
	QuantityLots uint64
	MakerFee     uint64
	TakerFee     uint64
}

// BatchEvent carries the attributes the chain emitted when a batch is finalized.
type BatchEvent struct {
	Number       uint64
	BatchID      []byte
	Commitment   []byte
	ResultsHash  []byte
	CommandCount uint64
	PreRevision  uint64
	PostRevision uint64
}
