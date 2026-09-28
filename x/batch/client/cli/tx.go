package cli

import (
	"encoding/hex"
	"fmt"
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
batch submitter. The submitter does not own the commands.

--previous-commitment is 32 bytes of hex. Batch 1 uses 64 zero digits.
A later batch must name the current head commitment.`,
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
			prev, err := cmd.Flags().GetString(flagPreviousCommitment)
			if err != nil {
				return err
			}
			if prev != "" {
				raw, err := hex.DecodeString(prev)
				if err != nil || len(raw) != 32 {
					return fmt.Errorf("previous commitment must be 32 bytes of hex")
				}
				msg.PreviousBatchCommitment = raw
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), &msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	cmd.Flags().String(flagPreviousCommitment, "", "previous batch commitment as 64 hex characters")
	return cmd
}

const flagPreviousCommitment = "previous-commitment"
