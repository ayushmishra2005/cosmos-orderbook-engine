package app

import (
	runtimev1alpha1 "cosmossdk.io/api/cosmos/app/runtime/v1alpha1"
	appv1alpha1 "cosmossdk.io/api/cosmos/app/v1alpha1"
	authmodulev1 "cosmossdk.io/api/cosmos/auth/module/v1"
	bankmodulev1 "cosmossdk.io/api/cosmos/bank/module/v1"
	consensusmodulev1 "cosmossdk.io/api/cosmos/consensus/module/v1"
	genutilmodulev1 "cosmossdk.io/api/cosmos/genutil/module/v1"
	stakingmodulev1 "cosmossdk.io/api/cosmos/staking/module/v1"
	txconfigv1 "cosmossdk.io/api/cosmos/tx/config/v1"
	"cosmossdk.io/core/appconfig"
	"cosmossdk.io/depinject"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	consensus "github.com/cosmos/cosmos-sdk/x/consensus/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"

	_ "github.com/cosmos/cosmos-sdk/x/auth"
	_ "github.com/cosmos/cosmos-sdk/x/auth/tx/config"
	_ "github.com/cosmos/cosmos-sdk/x/bank"
	_ "github.com/cosmos/cosmos-sdk/x/consensus"
	_ "github.com/cosmos/cosmos-sdk/x/genutil"
	_ "github.com/cosmos/cosmos-sdk/x/staking"
)

const (
	Name            = "cosmos-orderbook"
	Bech32Prefix    = "cosmos"
	DefaultNodeHome = ".cosmos-orderbook"
)

// AppConfig wires the modules required to run the exchange chain.
func AppConfig() depinject.Config {
	return depinject.Configs(
		appconfig.Compose(&appv1alpha1.Config{
			Modules: []*appv1alpha1.ModuleConfig{
				{
					Name: "runtime",
					Config: appconfig.WrapAny(&runtimev1alpha1.Module{
						AppName: Name,
						PreBlockers: []string{
							authtypes.ModuleName,
						},
						BeginBlockers: []string{
							stakingtypes.ModuleName,
							authtypes.ModuleName,
							banktypes.ModuleName,
							genutiltypes.ModuleName,
							consensus.ModuleName,
							exchangetypes.ModuleName,
						},
						EndBlockers: []string{
							stakingtypes.ModuleName,
							banktypes.ModuleName,
							authtypes.ModuleName,
							genutiltypes.ModuleName,
							consensus.ModuleName,
						},
						InitGenesis: []string{
							authtypes.ModuleName,
							banktypes.ModuleName,
							stakingtypes.ModuleName,
							genutiltypes.ModuleName,
							consensus.ModuleName,
							exchangetypes.ModuleName,
							batchtypes.ModuleName,
						},
						ExportGenesis: []string{
							authtypes.ModuleName,
							banktypes.ModuleName,
							stakingtypes.ModuleName,
							genutiltypes.ModuleName,
							consensus.ModuleName,
							exchangetypes.ModuleName,
							batchtypes.ModuleName,
						},
					}),
				},
				{
					Name: authtypes.ModuleName,
					Config: appconfig.WrapAny(&authmodulev1.Module{
						Bech32Prefix: Bech32Prefix,
						ModuleAccountPermissions: []*authmodulev1.ModuleAccountPermission{
							{Account: authtypes.FeeCollectorName},
							{Account: stakingtypes.BondedPoolName, Permissions: []string{authtypes.Burner, authtypes.Staking}},
							{Account: stakingtypes.NotBondedPoolName, Permissions: []string{authtypes.Burner, authtypes.Staking}},
							{Account: exchangetypes.ModuleName},
						},
					}),
				},
				{Name: banktypes.ModuleName, Config: appconfig.WrapAny(&bankmodulev1.Module{})},
				{Name: stakingtypes.ModuleName, Config: appconfig.WrapAny(&stakingmodulev1.Module{})},
				{Name: genutiltypes.ModuleName, Config: appconfig.WrapAny(&genutilmodulev1.Module{})},
				{Name: consensus.ModuleName, Config: appconfig.WrapAny(&consensusmodulev1.Module{})},
				{Name: "tx", Config: appconfig.WrapAny(&txconfigv1.Config{})},
			},
		}),
	)
}
