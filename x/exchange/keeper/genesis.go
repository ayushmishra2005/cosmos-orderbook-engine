package keeper

import (
	"bytes"
	"context"
	"sort"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// InitGenesis writes exchange genesis after the whole document validates.
// A validation error leaves the exchange store unchanged.
// Book, owner, client, and expiration indexes are rebuilt from active orders.
func (k Keeper) InitGenesis(ctx sdk.Context, gs v1.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if string(k.instanceID) != gs.InstanceId {
		return types.ErrInstance
	}
	if err := gs.ValidateOrderIDs(k.chainID); err != nil {
		return err
	}
	if err := k.requireBacking(ctx, gs); err != nil {
		return err
	}
	orders, err := gs.ActiveOrders()
	if err != nil {
		return err
	}
	trades, err := gs.StoredTrades()
	if err != nil {
		return err
	}
	sort.Slice(orders, func(i, j int) bool {
		return bytes.Compare(orders[i].Order.Order.ID[:], orders[j].Order.Order.ID[:]) < 0
	})
	sort.Slice(trades, func(i, j int) bool {
		if trades[i].MarketID != trades[j].MarketID {
			return trades[i].MarketID < trades[j].MarketID
		}
		return trades[i].Sequence < trades[j].Sequence
	})
	return k.commit(ctx, func(ctx sdk.Context) error {
		return k.writeGenesis(ctx, gs, orders, trades)
	})
}

func (k Keeper) writeGenesis(ctx sdk.Context, gs v1.GenesisState, orders []v1.GenesisActiveOrder, trades []types.Trade) error {
	for _, asset := range gs.Assets {
		if err := k.initAsset(ctx, types.Asset{ID: domain.AssetID(asset.Id), Denom: asset.Denom}); err != nil {
			return err
		}
	}
	for _, market := range gs.Markets {
		if err := k.initMarket(ctx, marketFromProto(market)); err != nil {
			return err
		}
	}
	for _, bal := range gs.Balances {
		owner, err := sdk.AccAddressFromBech32(bal.Owner)
		if err != nil {
			return err
		}
		key, err := canonical.EncodeBalanceKey(owner, domain.AssetID(bal.AssetId))
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, key); err != nil {
			return err
		}
		if err := k.setBalance(ctx, owner, domain.AssetID(bal.AssetId), types.Balance{
			Available: bal.Available,
			Locked:    bal.Locked,
		}); err != nil {
			return err
		}
	}
	for _, nonce := range gs.Nonces {
		owner, err := sdk.AccAddressFromBech32(nonce.Owner)
		if err != nil {
			return err
		}
		key, err := canonical.EncodeAccountNonceKey(owner)
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, key); err != nil {
			return err
		}
		if err := k.setUint64(ctx, key, nonce.Nonce); err != nil {
			return err
		}
	}
	for _, seq := range gs.OrderSequences {
		key, err := canonical.EncodeMarketSequenceKey(domain.MarketID(seq.MarketId))
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, key); err != nil {
			return err
		}
		if err := k.setUint64(ctx, key, seq.Sequence); err != nil {
			return err
		}
	}
	for _, seq := range gs.TradeSequences {
		key, err := canonical.EncodeTradeSequenceKey(domain.MarketID(seq.MarketId))
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, key); err != nil {
			return err
		}
		if err := k.setUint64(ctx, key, seq.Sequence); err != nil {
			return err
		}
	}
	if gs.Revision > 0 {
		key := canonical.EncodeExchangeRevisionKey()
		if err := k.ensureAbsent(ctx, key); err != nil {
			return err
		}
		if err := k.setUint64(ctx, key, gs.Revision); err != nil {
			return err
		}
	}
	for _, order := range orders {
		if err := k.writeGenesisOrder(ctx, order); err != nil {
			return err
		}
	}
	for _, trade := range trades {
		key, err := canonical.EncodeTradeKey(trade.MarketID, trade.Sequence)
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, key); err != nil {
			return err
		}
		bz, err := types.EncodeTrade(trade)
		if err != nil {
			return err
		}
		kv, err := k.kv(ctx)
		if err != nil {
			return err
		}
		if err := kv.Set(key, bz); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) writeGenesisOrder(ctx context.Context, order v1.GenesisActiveOrder) error {
	active, err := canonical.EncodeActiveOrderKey(order.Order.Order.ID)
	if err != nil {
		return err
	}
	if err := k.ensureAbsent(ctx, active); err != nil {
		return err
	}
	book, err := bookKey(order.Order.Order)
	if err != nil {
		return err
	}
	if err := k.ensureAbsent(ctx, book); err != nil {
		return err
	}
	ownerKey, err := canonical.EncodeOwnerOpenOrderKey(order.Order.Order.Owner, order.Order.Order.MarketID, order.Order.Order.ID)
	if err != nil {
		return err
	}
	if err := k.ensureAbsent(ctx, ownerKey); err != nil {
		return err
	}
	if order.Order.Order.TimeInForce == domain.TimeInForceGTD {
		expKey, err := canonical.EncodeExpirationKey(order.Order.Order.ExpiryHeight, order.Order.Order.ID)
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, expKey); err != nil {
			return err
		}
	}
	var clientKey []byte
	if len(order.ClientOrderID) > 0 {
		clientKey, err = canonical.EncodeActiveClientOrderKey(order.Order.Order.Owner, order.ClientOrderID)
		if err != nil {
			return err
		}
		if err := k.ensureAbsent(ctx, clientKey); err != nil {
			return err
		}
	}
	if err := k.putResting(ctx, order.Order); err != nil {
		return err
	}
	if len(clientKey) == 0 {
		return nil
	}
	kv, err := k.kv(ctx)
	if err != nil {
		return err
	}
	return kv.Set(clientKey, append([]byte(nil), order.Order.Order.ID[:]...))
}

func (k Keeper) ensureAbsent(ctx context.Context, key []byte) error {
	existing, err := k.get(ctx, key)
	if err != nil {
		return err
	}
	if existing != nil {
		return types.ErrExists
	}
	return nil
}

func (k Keeper) requireBacking(ctx context.Context, gs v1.GenesisState) error {
	if k.bank == nil {
		return types.ErrCorrupt
	}
	assets := make(map[uint64]string, len(gs.Assets))
	for _, asset := range gs.Assets {
		assets[asset.Id] = asset.Denom
	}
	sums := make(map[uint64]uint64)
	for _, bal := range gs.Balances {
		row, err := arithmetic.Add(bal.Available, bal.Locked)
		if err != nil {
			return err
		}
		next, err := arithmetic.Add(sums[bal.AssetId], row)
		if err != nil {
			return err
		}
		sums[bal.AssetId] = next
	}
	module := ModuleAddress()
	for id, sum := range sums {
		coin := k.bank.GetBalance(ctx, module, assets[id])
		if coin.Amount.LT(sdkmath.NewIntFromUint64(sum)) {
			return types.ErrUnbacked
		}
	}
	return nil
}

// ExportGenesis reads exchange state in key order.
// Secondary indexes are omitted. Client order IDs are copied onto the
// active orders so InitGenesis can rebuild that index.
func (k Keeper) ExportGenesis(ctx context.Context) (v1.GenesisState, error) {
	gs := v1.GenesisState{InstanceId: string(k.instanceID)}
	err := k.iteratePrefix(ctx, []byte{canonical.PrefixAsset}, func(_, value []byte) (bool, error) {
		asset, err := types.DecodeAsset(value)
		if err != nil {
			return false, err
		}
		gs.Assets = append(gs.Assets, &v1.Asset{Id: uint64(asset.ID), Denom: asset.Denom})
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	denoms := make(map[domain.AssetID]string, len(gs.Assets))
	for _, asset := range gs.Assets {
		denoms[domain.AssetID(asset.Id)] = asset.Denom
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixMarket}, func(_, value []byte) (bool, error) {
		market, err := types.DecodeMarket(value)
		if err != nil {
			return false, err
		}
		gs.Markets = append(gs.Markets, marketToProto(market, denoms))
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixBalance}, func(key, value []byte) (bool, error) {
		owner, assetID, err := canonical.DecodeBalanceKey(key)
		if err != nil {
			return false, err
		}
		bal, err := types.DecodeBalance(value)
		if err != nil {
			return false, err
		}
		gs.Balances = append(gs.Balances, &v1.GenesisBalance{
			Owner:     addrString(owner),
			AssetId:   uint64(assetID),
			Available: bal.Available,
			Locked:    bal.Locked,
		})
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixAccountNonce}, func(key, value []byte) (bool, error) {
		owner, err := canonical.DecodeAccountNonceKey(key)
		if err != nil {
			return false, err
		}
		nonce, err := types.DecodeUint64(value)
		if err != nil {
			return false, err
		}
		gs.Nonces = append(gs.Nonces, &v1.AccountNonce{Owner: addrString(owner), Nonce: nonce})
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixMarketSequence}, func(key, value []byte) (bool, error) {
		id, err := canonical.DecodeMarketSequenceKey(key)
		if err != nil {
			return false, err
		}
		seq, err := types.DecodeUint64(value)
		if err != nil {
			return false, err
		}
		gs.OrderSequences = append(gs.OrderSequences, &v1.MarketSequence{MarketId: uint64(id), Sequence: seq})
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixTradeSequence}, func(key, value []byte) (bool, error) {
		id, err := canonical.DecodeTradeSequenceKey(key)
		if err != nil {
			return false, err
		}
		seq, err := types.DecodeUint64(value)
		if err != nil {
			return false, err
		}
		gs.TradeSequences = append(gs.TradeSequences, &v1.MarketSequence{MarketId: uint64(id), Sequence: seq})
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	clients := make(map[domain.OrderID]clientRef)
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixActiveClientOrder}, func(key, value []byte) (bool, error) {
		owner, clientID, err := canonical.DecodeActiveClientOrderKey(key)
		if err != nil {
			return false, err
		}
		if len(value) != len(domain.OrderID{}) {
			return false, types.ErrCorrupt
		}
		var id domain.OrderID
		copy(id[:], value)
		if _, ok := clients[id]; ok {
			return false, types.ErrCorrupt
		}
		clients[id] = clientRef{
			owner:  append([]byte(nil), owner...),
			client: append([]byte(nil), clientID...),
		}
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixActiveOrder}, func(key, value []byte) (bool, error) {
		id, err := canonical.DecodeActiveOrderKey(key)
		if err != nil {
			return false, err
		}
		order, err := types.DecodeOrder(id, value)
		if err != nil {
			return false, err
		}
		var client []byte
		if ref, ok := clients[id]; ok {
			if !bytes.Equal(ref.owner, order.Order.Owner) {
				return false, types.ErrCorrupt
			}
			client = ref.client
			delete(clients, id)
		}
		gs.Orders = append(gs.Orders, genesisOrderToProto(order, client))
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	if len(clients) != 0 {
		return v1.GenesisState{}, types.ErrCorrupt
	}
	err = k.iteratePrefix(ctx, []byte{canonical.PrefixTrade}, func(key, value []byte) (bool, error) {
		marketID, sequence, err := canonical.DecodeTradeKey(key)
		if err != nil {
			return false, err
		}
		trade, err := types.DecodeTrade(value)
		if err != nil {
			return false, err
		}
		if trade.MarketID != marketID || trade.Sequence != sequence {
			return false, types.ErrCorrupt
		}
		gs.Trades = append(gs.Trades, tradeProto(trade))
		return false, nil
	})
	if err != nil {
		return v1.GenesisState{}, err
	}
	sort.Slice(gs.Orders, func(i, j int) bool {
		return bytes.Compare(gs.Orders[i].OrderId, gs.Orders[j].OrderId) < 0
	})
	sort.Slice(gs.Trades, func(i, j int) bool {
		if gs.Trades[i].MarketId != gs.Trades[j].MarketId {
			return gs.Trades[i].MarketId < gs.Trades[j].MarketId
		}
		return gs.Trades[i].Sequence < gs.Trades[j].Sequence
	})
	rev, err := k.GetRevision(ctx)
	if err != nil {
		return v1.GenesisState{}, err
	}
	gs.Revision = rev
	return gs, nil
}

type clientRef struct {
	owner  []byte
	client []byte
}

func genesisOrderToProto(order types.StoredOrder, client []byte) *v1.GenesisOrder {
	o := order.Order
	return &v1.GenesisOrder{
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
		TakerGross:    order.TakerGross,
		MakerGross:    order.MakerGross,
		ClientOrderId: append([]byte(nil), client...),
	}
}

func marketFromProto(m *v1.Market) types.Market {
	return types.Market{
		ID:                      domain.MarketID(m.Id),
		BaseAssetID:             domain.AssetID(m.BaseAssetId),
		QuoteAssetID:            domain.AssetID(m.QuoteAssetId),
		BaseLotSize:             m.BaseLotSize,
		QuoteAtomsPerTickPerLot: m.QuoteAtomsPerTickPerLot,
		MakerFeePPM:             m.MakerFeePpm,
		TakerFeePPM:             m.TakerFeePpm,
		MaxMakerVisits:          m.MaxMakerVisits,
		Enabled:                 m.Enabled,
	}
}

func marketToProto(m types.Market, denoms map[domain.AssetID]string) *v1.Market {
	return &v1.Market{
		Id:                      uint64(m.ID),
		BaseAssetId:             uint64(m.BaseAssetID),
		QuoteAssetId:            uint64(m.QuoteAssetID),
		BaseLotSize:             m.BaseLotSize,
		QuoteAtomsPerTickPerLot: m.QuoteAtomsPerTickPerLot,
		MakerFeePpm:             m.MakerFeePPM,
		TakerFeePpm:             m.TakerFeePPM,
		MaxMakerVisits:          m.MaxMakerVisits,
		Enabled:                 m.Enabled,
		BaseDenom:               denoms[m.BaseAssetID],
		QuoteDenom:              denoms[m.QuoteAssetID],
	}
}
