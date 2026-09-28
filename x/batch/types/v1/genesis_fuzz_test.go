package v1

import "testing"

func FuzzBatchGenesisValidate(f *testing.F) {
	f.Add("cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0knp6y", uint64(0))
	f.Add("not-an-address", uint64(1))
	f.Fuzz(func(t *testing.T, submitter string, latest uint64) {
		gs := GenesisState{Submitter: submitter, LatestBatchNumber: latest}
		if latest > 4 {
			latest = latest % 4
			gs.LatestBatchNumber = latest
		}
		_ = gs.Validate()
	})
}
