package app

import (
	"cosmossdk.io/depinject"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	genutil "github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch"
	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	batchv1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// EncodingConfig is the client codec set.
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
	BasicManager      module.BasicManager
}

// MakeEncodingConfig builds the client encoding without opening a database.
func MakeEncodingConfig(logger log.Logger) (EncodingConfig, error) {
	var enc EncodingConfig
	if err := depinject.Inject(
		depinject.Configs(AppConfig(), depinject.Supply(logger)),
		&enc.Codec,
		&enc.Amino,
		&enc.InterfaceRegistry,
		&enc.TxConfig,
		&enc.BasicManager,
	); err != nil {
		return EncodingConfig{}, err
	}
	v1.RegisterInterfaces(enc.InterfaceRegistry)
	batchv1.RegisterInterfaces(enc.InterfaceRegistry)
	enc.BasicManager[exchangetypes.ModuleName] = exchange.AppModuleBasic{}
	enc.BasicManager[batchtypes.ModuleName] = batch.AppModuleBasic{}
	enc.BasicManager[genutiltypes.ModuleName] = genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator)
	return enc, nil
}
