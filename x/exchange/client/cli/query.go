package cli

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// GetQueryCmd returns exchange query commands.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        exchangetypes.ModuleName,
		Short:                      "Query exchange state",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		marketCmd(),
		marketsCmd(),
		balanceCmd(),
		balancesCmd(),
		orderCmd(),
		openOrdersCmd(),
		orderbookCmd(),
		tradesCmd(),
		revisionCmd(),
	)
	return cmd
}

func marketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "market [market-id]",
		Short: "Query one market",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := parseU64(args[0])
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Market(cmd.Context(), &v1.QueryMarketRequest{MarketId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func marketsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "markets",
		Short: "Query markets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			limit, offset, err := pageFlags(cmd)
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Markets(cmd.Context(), &v1.QueryMarketsRequest{Limit: limit, Offset: offset})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	addPageFlags(cmd)
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func balanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "balance [owner] [asset-id]",
		Short: "Query one exchange balance",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := parseU64(args[1])
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Balance(cmd.Context(), &v1.QueryBalanceRequest{Owner: args[0], AssetId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func balancesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "balances [owner]",
		Short: "Query an owner's exchange balances",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			limit, offset, err := pageFlags(cmd)
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Balances(cmd.Context(), &v1.QueryBalancesRequest{
				Owner: args[0], Limit: limit, Offset: offset,
			})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	addPageFlags(cmd)
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func orderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "order [order-id-hex]",
		Short: "Query one open order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := parseHex(args[0])
			if err != nil || len(id) != 32 {
				return fmt.Errorf("order id must be 32 bytes of hex")
			}
			res, err := v1.NewQueryClient(clientCtx).Order(cmd.Context(), &v1.QueryOrderRequest{OrderId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func openOrdersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open-orders [owner]",
		Short: "Query an owner's open orders",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			limit, offset, err := pageFlags(cmd)
			if err != nil {
				return err
			}
			market, err := cmd.Flags().GetUint64("market")
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).OpenOrders(cmd.Context(), &v1.QueryOpenOrdersRequest{
				Owner: args[0], MarketId: market, Limit: limit, Offset: offset,
			})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	cmd.Flags().Uint64("market", 0, "optional market id filter")
	addPageFlags(cmd)
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func orderbookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orderbook [market-id]",
		Short: "Query one market book in canonical order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := parseU64(args[0])
			if err != nil {
				return err
			}
			depth, err := cmd.Flags().GetUint32("depth")
			if err != nil {
				return err
			}
			askOffset, err := cmd.Flags().GetUint32("ask-offset")
			if err != nil {
				return err
			}
			bidOffset, err := cmd.Flags().GetUint32("bid-offset")
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Orderbook(cmd.Context(), &v1.QueryOrderbookRequest{
				MarketId: id, Depth: depth, AskOffset: askOffset, BidOffset: bidOffset,
			})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	cmd.Flags().Uint32("depth", 0, "orders per side, default 50, maximum 100")
	cmd.Flags().Uint32("ask-offset", 0, "asks to skip")
	cmd.Flags().Uint32("bid-offset", 0, "bids to skip")
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func tradesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trades [market-id]",
		Short: "Query trades for one market",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := parseU64(args[0])
			if err != nil {
				return err
			}
			after, err := cmd.Flags().GetUint64("after")
			if err != nil {
				return err
			}
			limit, err := cmd.Flags().GetUint32("limit")
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Trades(cmd.Context(), &v1.QueryTradesRequest{
				MarketId: id, AfterSequence: after, Limit: limit,
			})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	cmd.Flags().Uint64("after", 0, "return trades after this sequence")
	cmd.Flags().Uint32("limit", 0, "page size, default 50, maximum 100")
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func revisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revision",
		Short: "Query the exchange revision",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).ExchangeRevision(cmd.Context(), &v1.QueryExchangeRevisionRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func addPageFlags(cmd *cobra.Command) {
	cmd.Flags().Uint32("limit", 0, "page size, default 50, maximum 100")
	cmd.Flags().Uint64("offset", 0, "records to skip")
}

func pageFlags(cmd *cobra.Command) (uint32, uint64, error) {
	limit, err := cmd.Flags().GetUint32("limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err := cmd.Flags().GetUint64("offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

func parseHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}
