package main

import (
	"os"
	"path/filepath"

	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/spf13/cast"
	"github.com/spf13/cobra"

	"cosmossdk.io/core/address"
	"cosmossdk.io/log/v2"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/config"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	sdkaddress "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/server"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authcli "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankcli "github.com/cosmos/cosmos-sdk/x/bank/client/cli"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/app"
	batchcli "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/client/cli"
	exchangecli "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/client/cli"
)

// NewRootCmd builds the node CLI.
func NewRootCmd() *cobra.Command {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(app.Bech32Prefix, app.Bech32Prefix+"pub")
	cfg.SetBech32PrefixForValidator(app.Bech32Prefix+"valoper", app.Bech32Prefix+"valoperpub")
	cfg.SetBech32PrefixForConsensusNode(app.Bech32Prefix+"valcons", app.Bech32Prefix+"valconspub")
	cfg.Seal()

	enc, err := app.MakeEncodingConfig(log.NewNopLogger())
	if err != nil {
		panic(err)
	}
	defaultHome, err := defaultHome()
	if err != nil {
		panic(err)
	}
	initClientCtx := client.Context{}.
		WithCodec(enc.Codec).
		WithInterfaceRegistry(enc.InterfaceRegistry).
		WithTxConfig(enc.TxConfig).
		WithLegacyAmino(enc.Amino).
		WithInput(os.Stdin).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithHomeDir(defaultHome).
		WithViper("")

	rootCmd := &cobra.Command{
		Use:   "cosmos-orderbookd",
		Short: "Cosmos order book chain",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SetOut(cmd.OutOrStdout())
			cmd.SetErr(cmd.ErrOrStderr())
			clientCtx, err := client.ReadPersistentCommandFlags(initClientCtx, cmd.Flags())
			if err != nil {
				return err
			}
			clientCtx, err = config.ReadFromClientConfig(clientCtx)
			if err != nil {
				return err
			}
			if err := client.SetCmdClientContextHandler(clientCtx, cmd); err != nil {
				return err
			}
			return server.InterceptConfigsPreRunHandler(cmd, "", nil, cmtcfg.DefaultConfig())
		},
	}

	rootCmd.AddCommand(
		genutilcli.InitCmd(enc.BasicManager, defaultHome),
		genutilcli.Commands(enc.TxConfig, enc.BasicManager, defaultHome),
		queryCommand(),
		txCommand(sdkaddress.NewBech32Codec(app.Bech32Prefix)),
		keys.Commands(),
	)
	server.AddCommands(rootCmd, defaultHome, newApp, appExport, func(*cobra.Command) {})
	return rootCmd
}

func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Query subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		rpc.ValidatorCommand(),
		authcli.QueryTxCmd(),
		authcli.QueryTxsByEventsCmd(),
		exchangecli.GetQueryCmd(),
		batchcli.GetQueryCmd(),
	)
	cmd.PersistentFlags().String(flags.FlagChainID, "", "network chain ID")
	return cmd
}

func txCommand(ac address.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		authcli.GetSignCommand(),
		authcli.GetSignBatchCommand(),
		authcli.GetBroadcastCommand(),
		authcli.GetEncodeCommand(),
		authcli.GetDecodeCommand(),
		authcli.GetSimulateCmd(),
		bankcli.NewTxCmd(ac),
		exchangecli.GetTxCmd(),
		batchcli.GetTxCmd(),
	)
	return cmd
}

func newApp(logger log.Logger, db dbm.DB, opts servertypes.AppOptions) servertypes.Application {
	home := cast.ToString(opts.Get(flags.FlagHome))
	chainID, err := app.ChainIDFromHome(home)
	if err != nil {
		panic(err)
	}
	application, err := app.New(logger, db, chainID, server.DefaultBaseappOptions(opts)...)
	if err != nil {
		panic(err)
	}
	return application
}

func appExport(logger log.Logger, db dbm.DB, height int64, forZeroHeight bool, jailAllowedAddrs []string, opts servertypes.AppOptions, modulesToExport []string) (servertypes.ExportedApp, error) {
	home := cast.ToString(opts.Get(flags.FlagHome))
	chainID, err := app.ChainIDFromHome(home)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	application, err := app.New(logger, db, chainID)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	if height != -1 {
		if err := application.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
	}
	return application.ExportAppStateAndValidators(forZeroHeight, jailAllowedAddrs, modulesToExport)
}

func defaultHome() (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, app.DefaultNodeHome), nil
}

// Execute runs the root command with the server and client contexts installed.
func Execute(defaultHome string) error {
	root := NewRootCmd()
	return svrcmd.Execute(root, "", defaultHome)
}
