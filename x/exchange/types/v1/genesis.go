package v1

import (
	"fmt"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// DefaultInstanceID is mixed into every order ID for this application.
const DefaultInstanceID = "orderbook-v1"

// DefaultGenesis is the genesis used by a new chain.
// Bank accounts are funded separately and then deposited.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		InstanceId: DefaultInstanceID,
		Assets: []*Asset{
			{Id: 1, Denom: "base"},
			{Id: 2, Denom: "quote"},
		},
		Markets: []*Market{{
			Id:                      1,
			BaseAssetId:             1,
			QuoteAssetId:            2,
			BaseLotSize:             1,
			QuoteAtomsPerTickPerLot: 1,
			MaxMakerVisits:          64,
			Enabled:                 true,
		}},
	}
}

// Validate checks genesis structure before any store write.
// Bank custody is checked separately at init. Invalid orders and
// locked balances are rejected, not repaired.
func (gs GenesisState) Validate() error {
	if gs.InstanceId != DefaultInstanceID {
		return exchangetypes.ErrInstance
	}
	assets := make(map[uint64]string, len(gs.Assets))
	denoms := make(map[string]struct{}, len(gs.Assets))
	for _, asset := range gs.Assets {
		if asset == nil {
			return fmt.Errorf("nil asset")
		}
		if err := (exchangetypes.Asset{ID: domain.AssetID(asset.Id), Denom: asset.Denom}).Validate(); err != nil {
			return err
		}
		if _, ok := assets[asset.Id]; ok {
			return exchangetypes.ErrExists
		}
		if _, ok := denoms[asset.Denom]; ok {
			return exchangetypes.ErrExists
		}
		assets[asset.Id] = asset.Denom
		denoms[asset.Denom] = struct{}{}
	}
	markets := make(map[domain.MarketID]exchangetypes.Market, len(gs.Markets))
	for _, market := range gs.Markets {
		if market == nil {
			return fmt.Errorf("nil market")
		}
		if _, ok := assets[market.BaseAssetId]; !ok {
			return domain.ErrInvalidAsset
		}
		if _, ok := assets[market.QuoteAssetId]; !ok {
			return domain.ErrInvalidAsset
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
			return err
		}
		if _, ok := markets[parsed.ID]; ok {
			return exchangetypes.ErrExists
		}
		markets[parsed.ID] = parsed
	}
	orders, err := gs.activeOrders()
	if err != nil {
		return err
	}
	trades, err := gs.storedTrades(markets)
	if err != nil {
		return err
	}
	if err := gs.validateBalances(assets, orders, markets); err != nil {
		return err
	}
	if err := gs.validateNonces(orders); err != nil {
		return err
	}
	if err := gs.validateSequences(markets, orders, trades); err != nil {
		return err
	}
	return validateFeeGross(orders, trades)
}
