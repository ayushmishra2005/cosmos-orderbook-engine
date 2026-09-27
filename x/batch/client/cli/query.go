package cli

import (
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

// GetQueryCmd returns batch query commands.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        batchtypes.ModuleName,
		Short:                      "Query batch state",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(batchCmd(), latestCmd(), byIDCmd())
	return cmd
}

func batchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batch [batch-number]",
		Short: "Query one finalized batch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			number, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).Batch(cmd.Context(), &v1.QueryBatchRequest{BatchNumber: number})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func latestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "latest",
		Short: "Query the latest finalized batch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			res, err := v1.NewQueryClient(clientCtx).LatestBatch(cmd.Context(), &v1.QueryLatestBatchRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func byIDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "by-id [batch-id-hex]",
		Short: "Query a batch by its batch ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := hex.DecodeString(args[0])
			if err != nil || len(id) != 32 {
				return fmt.Errorf("batch id must be 32 bytes of hex")
			}
			res, err := v1.NewQueryClient(clientCtx).BatchByID(cmd.Context(), &v1.QueryBatchByIDRequest{BatchId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
