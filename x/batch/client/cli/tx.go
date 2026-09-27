package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
)

// GetTxCmd returns batch transaction commands.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        batchtypes.ModuleName,
		Short:                      "Batch transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(finalizeCmd())
	return cmd
}

func finalizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "finalize [batch.json]",
		Short: "Finalize a signed batch from proto JSON",
		Long: `The file is proto JSON for MsgFinalizeBatch.

Each command is signed by its owner with secp256k1 over the canonical
batch-command bytes, not over this JSON. --from must be the authorized
batch submitter. The submitter does not own the commands.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			bz, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var msg v1.MsgFinalizeBatch
			if err := clientCtx.Codec.UnmarshalJSON(bz, &msg); err != nil {
				return err
			}
			msg.Submitter = clientCtx.GetFromAddress().String()
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), &msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
