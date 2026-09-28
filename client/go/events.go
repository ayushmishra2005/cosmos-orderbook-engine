package orderbook

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	rpcjson "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func decodeMessage(msg []byte) ([]Event, error) {
	if len(msg) == 0 {
		return nil, fmt.Errorf("empty event")
	}
	var resp rpcjson.RPCResponse
	if err := json.Unmarshal(msg, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Error())
	}
	if len(resp.Result) == 0 || string(resp.Result) == "{}" || string(resp.Result) == "null" {
		return nil, nil
	}
	var result coretypes.ResultEvent
	if err := cmtjson.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return eventsFromResult(result)
}

func eventsFromResult(result coretypes.ResultEvent) ([]Event, error) {
	switch data := result.Data.(type) {
	case cmttypes.EventDataNewBlock:
		return eventsFromNewBlock(data), nil
	case *cmttypes.EventDataNewBlock:
		if data == nil {
			return nil, nil
		}
		return eventsFromNewBlock(*data), nil
	case cmttypes.EventDataTx:
		hash := fmt.Sprintf("%X", cmttypes.Tx(data.Tx).Hash())
		return eventsFromABCI(data.Height, hash, data.Result.Events), nil
	case *cmttypes.EventDataTx:
		if data == nil {
			return nil, nil
		}
		hash := fmt.Sprintf("%X", cmttypes.Tx(data.Tx).Hash())
		return eventsFromABCI(data.Height, hash, data.Result.Events), nil
	case cmttypes.EventDataNewBlockEvents:
		return eventsFromABCI(data.Height, "", data.Events), nil
	case *cmttypes.EventDataNewBlockEvents:
		if data == nil {
			return nil, nil
		}
		return eventsFromABCI(data.Height, "", data.Events), nil
	default:
		return nil, nil
	}
}

func eventsFromNewBlock(data cmttypes.EventDataNewBlock) []Event {
	if data.Block == nil {
		return nil
	}
	height := data.Block.Height
	out := eventsFromABCI(height, "", data.ResultFinalizeBlock.Events)
	for i, result := range data.ResultFinalizeBlock.TxResults {
		if result == nil {
			continue
		}
		hash := ""
		if i < len(data.Block.Txs) {
			hash = fmt.Sprintf("%X", data.Block.Txs[i].Hash())
		}
		out = append(out, eventsFromABCI(height, hash, result.Events)...)
	}
	return out
}

func eventsFromABCI(height int64, txHash string, events []abci.Event) []Event {
	out := make([]Event, 0, len(events))
	for i := range events {
		ev, ok := parseEvent(height, txHash, i, events[i])
		if ok {
			out = append(out, ev)
		}
	}
	return out
}

func parseEvent(height int64, txHash string, index int, raw abci.Event) (Event, bool) {
	attrs := make(map[string]string, len(raw.Attributes))
	for _, attr := range raw.Attributes {
		attrs[attr.Key] = attr.Value
	}
	ev := Event{ID: EventID{Height: height, TxHash: txHash, Index: index}, Kind: Kind(raw.Type)}
	switch raw.Type {
	case exchangetypes.EventTypeOrderAccepted:
		order, ok := parseOrder(attrs, true)
		if !ok {
			return Event{}, false
		}
		ev.Order = order
	case exchangetypes.EventTypeOrderPartiallyFilled, exchangetypes.EventTypeOrderFilled:
		order, ok := parseOrder(attrs, false)
		if !ok {
			return Event{}, false
		}
		ev.Order = order
	case exchangetypes.EventTypeOrderCancelled, exchangetypes.EventTypeOrderExpired:
		order, ok := parseOrder(attrs, false)
		if !ok {
			return Event{}, false
		}
		ev.Order = order
	case exchangetypes.EventTypeTrade:
		trade, ok := parseTrade(attrs)
		if !ok {
			return Event{}, false
		}
		ev.Trade = trade
	case batchtypes.EventTypeBatchFinalized:
		batch, ok := parseBatch(attrs)
		if !ok {
			return Event{}, false
		}
		ev.Batch = batch
	default:
		return Event{}, false
	}
	return ev, true
}

func parseOrder(attrs map[string]string, accepted bool) (*OrderEvent, bool) {
	id, ok := decodeHex(attrs["order_id"])
	if !ok || len(id) != 32 {
		return nil, false
	}
	out := &OrderEvent{OrderID: id, Owner: attrs["owner"]}
	if raw, exists := attrs["market_id"]; exists {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		out.MarketID = n
	}
	if raw, exists := attrs["remaining"]; exists {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		out.Remaining = n
	}
	if raw, exists := attrs["asset_id"]; exists {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		out.AssetID = n
	}
	if raw, exists := attrs["released"]; exists {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		out.Released = n
	}
	if accepted {
		rested, err := strconv.ParseBool(attrs["rested"])
		if err != nil {
			return nil, false
		}
		out.Rested = rested
	}
	return out, true
}

func parseTrade(attrs map[string]string) (*TradeEvent, bool) {
	market, err1 := strconv.ParseUint(attrs["market_id"], 10, 64)
	seq, err2 := strconv.ParseUint(attrs["sequence"], 10, 64)
	price, err3 := strconv.ParseUint(attrs["price"], 10, 64)
	qty, err4 := strconv.ParseUint(attrs["quantity"], 10, 64)
	maker, err5 := strconv.ParseUint(attrs["maker_fee"], 10, 64)
	taker, err6 := strconv.ParseUint(attrs["taker_fee"], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
		return nil, false
	}
	if market == 0 || seq == 0 || price == 0 || qty == 0 {
		return nil, false
	}
	return &TradeEvent{
		MarketID: market, Sequence: seq, PriceTicks: price, QuantityLots: qty,
		MakerFee: maker, TakerFee: taker,
	}, true
}

func parseBatch(attrs map[string]string) (*BatchEvent, bool) {
	number, err1 := strconv.ParseUint(attrs["batch_number"], 10, 64)
	count, err2 := strconv.ParseUint(attrs["command_count"], 10, 64)
	pre, err3 := strconv.ParseUint(attrs["pre_revision"], 10, 64)
	post, err4 := strconv.ParseUint(attrs["post_revision"], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || number == 0 {
		return nil, false
	}
	id, ok1 := decodeHex(attrs["batch_id"])
	commitment, ok2 := decodeHex(attrs["batch_commitment"])
	results, ok3 := decodeHex(attrs["results_hash"])
	if !ok1 || !ok2 || !ok3 || len(id) != 32 || len(commitment) != 32 || len(results) != 32 {
		return nil, false
	}
	return &BatchEvent{
		Number: number, BatchID: id, Commitment: commitment, ResultsHash: results,
		CommandCount: count, PreRevision: pre, PostRevision: post,
	}, true
}

func decodeHex(s string) ([]byte, bool) {
	if s == "" || len(s)%2 != 0 {
		return nil, false
	}
	bz, err := hex.DecodeString(s)
	if err != nil {
		return nil, false
	}
	return bz, true
}

func eventMarket(ev Event) (uint64, bool) {
	switch ev.Kind {
	case KindTradeExecuted:
		if ev.Trade == nil {
			return 0, false
		}
		return ev.Trade.MarketID, true
	case KindOrderAccepted, KindOrderPartiallyFilled, KindOrderFilled, KindOrderCancelled, KindOrderExpired:
		if ev.Order == nil || ev.Order.MarketID == 0 {
			return 0, false
		}
		return ev.Order.MarketID, true
	default:
		return 0, false
	}
}
