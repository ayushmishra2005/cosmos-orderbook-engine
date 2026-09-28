package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cosmossdk.io/log/v2"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/app"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/sequencer"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "orderbook-sequencer: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("orderbook-sequencer", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", env("ORDERBOOK_NODE", "http://127.0.0.1:26657"), "Comet RPC endpoint")
	chainID := fs.String("chain-id", env("ORDERBOOK_CHAIN_ID", "orderbook-1"), "chain id")
	instance := fs.String("instance-id", env("ORDERBOOK_INSTANCE_ID", exchangev1.DefaultInstanceID), "exchange instance id")
	journal := fs.String("journal", env("ORDERBOOK_JOURNAL", "sequencer.journal"), "append-only journal path")
	listen := fs.String("listen", env("ORDERBOOK_LISTEN", "127.0.0.1:8080"), "admission HTTP listen address")
	from := fs.String("from", env("ORDERBOOK_FROM", "submitter"), "batch submitter key name")
	backend := fs.String("keyring-backend", env("ORDERBOOK_KEYRING_BACKEND", "test"), "keyring backend")
	home := fs.String("home", env("ORDERBOOK_HOME", ".localnet"), "node home used for the keyring")
	maxBatch := fs.Int("max-batch", envInt("ORDERBOOK_MAX_BATCH", 100), "maximum commands in one batch")
	interval := fs.Duration("batch-interval", envDuration("ORDERBOOK_BATCH_INTERVAL", 2*time.Second), "batch submission interval")
	retryInitial := fs.Duration("retry-initial", envDuration("ORDERBOOK_RETRY_INITIAL", time.Second), "initial delay after a failed batch submission")
	retryMax := fs.Duration("retry-max", envDuration("ORDERBOOK_RETRY_MAX", 30*time.Second), "maximum delay after a failed batch submission")
	maxBytes := fs.Int("max-command-bytes", envInt("ORDERBOOK_MAX_COMMAND_BYTES", 8192), "maximum encoded command size")
	maxOutstanding := fs.Int("max-outstanding", envInt("ORDERBOOK_MAX_OUTSTANDING", 10_000), "maximum pending and in-flight commands")
	maxPerOwner := fs.Int("max-per-owner", envInt("ORDERBOOK_MAX_PER_OWNER", 256), "maximum outstanding commands for one owner")
	maxPage := fs.Int("max-pending-page", envInt("ORDERBOOK_MAX_PENDING_PAGE", 100), "maximum /v1/pending page size")
	maxSeen := fs.Int("max-seen", envInt("ORDERBOOK_MAX_SEEN", 10_000), "finalized command ids retained for duplicate detection")
	fees := fs.String("fees", env("ORDERBOOK_FEES", "1stake"), "submitter transaction fee")
	gas := fs.Uint64("gas", envUint("ORDERBOOK_GAS", 2_000_000), "submitter transaction gas limit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	sealConfig()
	enc, err := app.MakeEncodingConfig(log.NewNopLogger())
	if err != nil {
		return err
	}
	chain, err := sequencer.NewCosmosChain(sequencer.CosmosConfig{
		Node:              *node,
		ChainID:           *chainID,
		Home:              *home,
		KeyringBackend:    *backend,
		FromName:          *from,
		Fees:              *fees,
		Gas:               *gas,
		Codec:             enc.Codec,
		InterfaceRegistry: enc.InterfaceRegistry,
		TxConfig:          enc.TxConfig,
	})
	if err != nil {
		return err
	}
	defer chain.Close()

	svc, err := sequencer.Open(sequencer.Config{
		ChainID:         *chainID,
		InstanceID:      []byte(*instance),
		Submitter:       chain.Submitter(),
		JournalPath:     *journal,
		MaxCommandBytes: *maxBytes,
		MaxBatch:        *maxBatch,
		MaxOutstanding:  *maxOutstanding,
		MaxPerOwner:     *maxPerOwner,
		MaxPendingPage:  *maxPage,
		MaxSeen:         *maxSeen,
		BatchInterval:   *interval,
		RetryInitial:    *retryInitial,
		RetryMax:        *retryMax,
	}, chain)
	if err != nil {
		return err
	}
	defer svc.Close()

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           svc.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "orderbook-sequencer: http: %v\n", err)
			stop()
		}
	}()
	fmt.Fprintf(os.Stderr, "orderbook-sequencer: listening on %s chain %s journal %s\n", *listen, *chainID, *journal)
	err = svc.Run(ctx)
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func sealConfig() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(app.Bech32Prefix, app.Bech32Prefix+"pub")
	cfg.SetBech32PrefixForValidator(app.Bech32Prefix+"valoper", app.Bech32Prefix+"valoperpub")
	cfg.SetBech32PrefixForConsensusNode(app.Bech32Prefix+"valcons", app.Bech32Prefix+"valconspub")
	cfg.Seal()
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscan(v, &n); err != nil {
		return fallback
	}
	return n
}

func envUint(key string, fallback uint64) uint64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n uint64
	if _, err := fmt.Sscan(v, &n); err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
