package app

import (
	"encoding/json"
	"fmt"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	staking "github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"

	"cosmossdk.io/depinject"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// App is the exchange chain.
type App struct {
	*runtime.App

	cdc           codec.Codec
	txConfig      client.TxConfig
	Keeper        exchangekeeper.Keeper
	BankKeeper    bankkeeper.BaseKeeper
	AccountKeeper authkeeper.AccountKeeper
	stakingKeeper *stakingkeeper.Keeper
}

// New builds the application and loads the latest version.
func New(logger log.Logger, db dbm.DB, chainID string) (*App, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain id is required")
	}
	if logger == nil {
		logger = log.NewNopLogger()
	}
	var (
		builder       *runtime.AppBuilder
		cdc           codec.Codec
		txConfig      client.TxConfig
		bank          bankkeeper.BaseKeeper
		account       authkeeper.AccountKeeper
		stakingKeeper *stakingkeeper.Keeper
	)
	if err := depinject.Inject(
		depinject.Configs(AppConfig(), depinject.Supply(logger)),
		&builder,
		&cdc,
		&txConfig,
		&bank,
		&account,
		&stakingKeeper,
	); err != nil {
		return nil, err
	}

	storeKey := storetypes.NewKVStoreKey(exchangetypes.StoreKey)
	k, err := exchangekeeper.NewKeeper(runtime.NewKVStoreService(storeKey), chainID, []byte(v1.DefaultInstanceID))
	if err != nil {
		return nil, err
	}
	k = k.WithBank(bank)

	app := &App{
		cdc:           cdc,
		txConfig:      txConfig,
		Keeper:        k,
		BankKeeper:    bank,
		AccountKeeper: account,
		stakingKeeper: stakingKeeper,
	}
	app.App = builder.Build(db, baseapp.SetChainID(chainID))
	if err := app.RegisterStores(storeKey); err != nil {
		return nil, err
	}
	if err := app.RegisterModules(exchange.NewAppModule(cdc, k)); err != nil {
		return nil, err
	}
	if err := app.Load(true); err != nil {
		return nil, err
	}
	return app, nil
}

// TxConfig returns the application transaction config.
func (app *App) TxConfig() client.TxConfig { return app.txConfig }

// Codec returns the protobuf codec.
func (app *App) Codec() codec.Codec { return app.cdc }

// ExportAppStateAndValidators writes the current application state.
func (app *App) ExportAppStateAndValidators(_ bool, _ []string, modulesToExport []string) (servertypes.ExportedApp, error) {
	ctx := app.NewContext(true)
	genState, err := app.ModuleManager.ExportGenesisForModules(ctx, app.cdc, modulesToExport)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	appState, err := json.MarshalIndent(genState, "", "  ")
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	validators, err := staking.WriteValidators(ctx, app.stakingKeeper)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	return servertypes.ExportedApp{
		AppState:        appState,
		Validators:      validators,
		Height:          app.LastBlockHeight(),
		ConsensusParams: app.GetConsensusParams(ctx),
	}, nil
}

// ConsensusParams is exported for tests that need the committed params.
func (app *App) ConsensusParams(ctx sdk.Context) cmtproto.ConsensusParams {
	return app.GetConsensusParams(ctx)
}
