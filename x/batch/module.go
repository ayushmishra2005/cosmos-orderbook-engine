package batch

import (
	"encoding/json"
	"fmt"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/keeper"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

const ConsensusVersion = 1

var (
	_ module.AppModuleBasic = AppModuleBasic{}
	_ module.AppModule      = AppModule{}
	_ module.HasGenesis     = AppModule{}
	_ module.HasServices    = AppModule{}
	_ appmodule.AppModule   = AppModule{}
)

// AppModuleBasic is the stateless batch module.
type AppModuleBasic struct{}

func (AppModuleBasic) Name() string { return types.ModuleName }

func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(r codectypes.InterfaceRegistry) {
	v1.RegisterInterfaces(r)
}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(v1.DefaultGenesis())
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	if len(bz) == 0 {
		return fmt.Errorf("batch genesis is empty")
	}
	var gs v1.GenesisState
	if err := cdc.UnmarshalJSON(bz, &gs); err != nil {
		return fmt.Errorf("unmarshal batch genesis: %w", err)
	}
	return gs.Validate()
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *runtime.ServeMux) {}

// AppModule is the batch module.
type AppModule struct {
	AppModuleBasic
	keeper keeper.Keeper
}

// NewAppModule returns the batch module.
func NewAppModule(_ codec.Codec, k keeper.Keeper) AppModule {
	return AppModule{keeper: k}
}

func (AppModule) IsOnePerModuleType() {}

func (AppModule) IsAppModule() {}

func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

func (am AppModule) RegisterServices(cfg module.Configurator) {
	v1.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServer(am.keeper))
	v1.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServer(am.keeper))
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, bz json.RawMessage) {
	var gs v1.GenesisState
	cdc.MustUnmarshalJSON(bz, &gs)
	if err := am.keeper.InitGenesis(ctx, gs); err != nil {
		panic(err)
	}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	gs, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	return cdc.MustMarshalJSON(&gs)
}
