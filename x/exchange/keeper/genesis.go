package keeper

import (
	"context"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// InitGenesis writes exchange genesis. Balances require bank custody.
func (k Keeper) InitGenesis(ctx sdk.Context, gs v1.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if string(k.instanceID) != gs.InstanceId {
		return types.ErrInstance
	}
	if err := k.requireBacking(ctx, gs); err != nil {
		return err
	}
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
		if err := k.setBalance(ctx, owner, domain.AssetID(bal.AssetId), types.Balance{Available: bal.Available}); err != nil {
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
		if err := k.setUint64(ctx, key, nonce.Nonce); err != nil {
			return err
		}
	}
	for _, seq := range gs.OrderSequences {
		key, err := canonical.EncodeMarketSequenceKey(domain.MarketID(seq.MarketId))
		if err != nil {
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
		if err := k.setUint64(ctx, key, seq.Sequence); err != nil {
			return err
		}
	}
	if gs.Revision > 0 {
		if err := k.setUint64(ctx, canonical.EncodeExchangeRevisionKey(), gs.Revision); err != nil {
			return err
		}
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
		next, err := arithmetic.Add(sums[bal.AssetId], bal.Available)
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
	rev, err := k.GetRevision(ctx)
	if err != nil {
		return v1.GenesisState{}, err
	}
	gs.Revision = rev
	return gs, nil
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
