package v1

import "testing"

func FuzzGenesisValidate(f *testing.F) {
	f.Add("orderbook-v1", "base", "quote", uint64(1), uint64(1), uint64(0), uint64(0))
	f.Add("other", "", "quote", uint64(0), uint64(1), uint64(2_000_000), uint64(1))
	f.Fuzz(func(t *testing.T, instance, base, quote string, lot, atoms, maker, taker uint64) {
		if len(base) > 64 {
			base = base[:64]
		}
		if len(quote) > 64 {
			quote = quote[:64]
		}
		gs := GenesisState{
			InstanceId: instance,
			Assets: []*Asset{
				{Id: 1, Denom: base},
				{Id: 2, Denom: quote},
			},
			Markets: []*Market{{
				Id: 1, BaseAssetId: 1, QuoteAssetId: 2,
				BaseLotSize: lot, QuoteAtomsPerTickPerLot: atoms,
				MakerFeePpm: maker, TakerFeePpm: taker,
				MaxMakerVisits: 8, Enabled: true,
			}},
		}
		_ = gs.Validate()
	})
}
