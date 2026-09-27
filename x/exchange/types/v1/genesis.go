package v1

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

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

// Validate checks genesis structure. Bank backing is checked at init.
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
	markets := make(map[uint64]struct{}, len(gs.Markets))
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
		if err := (exchangetypes.Market{
			ID:                      domain.MarketID(market.Id),
			BaseAssetID:             domain.AssetID(market.BaseAssetId),
			QuoteAssetID:            domain.AssetID(market.QuoteAssetId),
			BaseLotSize:             market.BaseLotSize,
			QuoteAtomsPerTickPerLot: market.QuoteAtomsPerTickPerLot,
			MakerFeePPM:             market.MakerFeePpm,
			TakerFeePPM:             market.TakerFeePpm,
			MaxMakerVisits:          market.MaxMakerVisits,
			Enabled:                 market.Enabled,
		}).Validate(); err != nil {
			return err
		}
		if _, ok := markets[market.Id]; ok {
			return exchangetypes.ErrExists
		}
		markets[market.Id] = struct{}{}
	}
	for _, bal := range gs.Balances {
		if bal == nil {
			return fmt.Errorf("nil balance")
		}
		if bal.Locked != 0 {
			return fmt.Errorf("genesis locked balance is not backed by an order")
		}
		if bal.Available == 0 {
			return exchangetypes.ErrInvalidAmount
		}
		if _, ok := assets[bal.AssetId]; !ok {
			return domain.ErrInvalidAsset
		}
		if _, err := sdk.AccAddressFromBech32(bal.Owner); err != nil {
			return err
		}
	}
	for _, nonce := range gs.Nonces {
		if nonce == nil || nonce.Nonce == 0 {
			return fmt.Errorf("command nonce must be positive")
		}
		if _, err := sdk.AccAddressFromBech32(nonce.Owner); err != nil {
			return err
		}
	}
	for _, seq := range append(append([]*MarketSequence{}, gs.OrderSequences...), gs.TradeSequences...) {
		if seq == nil || seq.Sequence == 0 {
			return domain.ErrInvalidSequence
		}
		if _, ok := markets[seq.MarketId]; !ok {
			return domain.ErrInvalidMarket
		}
	}
	return nil
}
