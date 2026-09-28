package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/app"
	orderbook "github.com/ayushmishra2005/cosmos-orderbook-engine/client/go"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"

	"cosmossdk.io/log/v2"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "orderbook-client: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("orderbook-client", flag.ContinueOnError)
	grpcAddr := fs.String("grpc", "127.0.0.1:9090", "exchange gRPC host:port")
	rpcAddr := fs.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC")
	chainID := fs.String("chain-id", "orderbook-1", "chain id")
	home := fs.String("home", ".localnet", "keyring home")
	name := fs.String("from", "alice", "key name")
	market := fs.Uint64("market", 1, "market id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	enc, err := app.MakeEncodingConfig(log.NewNopLogger())
	if err != nil {
		return err
	}
	kr, err := keyring.New(sdk.KeyringServiceName(), keyring.BackendTest, *home, os.Stdin, enc.Codec)
	if err != nil {
		return err
	}
	signer, err := orderbook.NewKeyringSigner(kr, *name)
	if err != nil {
		return err
	}
	client, err := orderbook.NewClient(orderbook.Config{
		GRPCEndpoint: *grpcAddr,
		RPCEndpoint:  *rpcAddr,
		ChainID:      *chainID,
		Fees:         "1stake",
	})
	if err != nil {
		return err
	}
	defer client.Close()
	client.WithSigner(signer)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := client.GetMarket(ctx, *market)
	if err != nil {
		return err
	}
	fmt.Printf("market %d %s/%s\n", got.ID, got.BaseDenom, got.QuoteDenom)

	trades, err := client.SubscribeTrades(ctx, *market)
	if err != nil {
		return err
	}
	go func() {
		for ev := range trades.Events {
			if ev.Trade == nil {
				continue
			}
			fmt.Printf("trade height=%d seq=%d price=%d qty=%d id=%d/%s/%d\n",
				ev.ID.Height, ev.Trade.Sequence, ev.Trade.PriceTicks, ev.Trade.QuantityLots,
				ev.ID.Height, ev.ID.TxHash, ev.ID.Index)
		}
	}()

	// CommandNonce must be the account's next exchange nonce. The chain assigns the order ID.
	_, err = client.PlaceLimitOrder(ctx, orderbook.LimitOrder{
		MarketID: *market, Side: domain.SideBuy, TimeInForce: domain.TimeInForceGTC,
		QuantityLots: 1, PriceTicks: 1, CommandNonce: 1,
	})
	return err
}
