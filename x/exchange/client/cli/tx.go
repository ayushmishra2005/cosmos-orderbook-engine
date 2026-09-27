package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"

	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

const (
	flagTimeInForce   = "time-in-force"
	flagExpiry        = "expiry"
	flagClientOrderID = "client-order-id"
)

// GetTxCmd returns exchange transaction commands.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        exchangetypes.ModuleName,
		Short:                      "Exchange transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		depositCmd(),
		withdrawCmd(),
		placeLimitCmd(),
		placeMarketCmd(),
		cancelCmd(),
	)
	return cmd
}

func depositCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deposit [amount]",
		Short: "Deposit bank coins into exchange custody",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			coin, err := sdk.ParseCoinNormalized(args[0])
			if err != nil {
				return err
			}
			msg := &v1.MsgDeposit{Owner: clientCtx.GetFromAddress().String(), Amount: coin}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func withdrawCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "withdraw [amount]",
		Short: "Withdraw available exchange balance to a bank account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			coin, err := sdk.ParseCoinNormalized(args[0])
			if err != nil {
				return err
			}
			msg := &v1.MsgWithdraw{Owner: clientCtx.GetFromAddress().String(), Amount: coin}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func placeLimitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "place-limit-order [market-id] [buy|sell] [quantity-lots] [price-ticks] [command-nonce]",
		Short: "Place a limit order",
		Args:  cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			return broadcastOrder(cmd, args, v1.OrderType_ORDER_TYPE_LIMIT, v1.TimeInForce_TIME_IN_FORCE_GTC)
		},
	}
	cmd.Flags().String(flagTimeInForce, "gtc", "gtc, ioc, fok, or gtd")
	cmd.Flags().Uint64(flagExpiry, 0, "GTD expiry height")
	cmd.Flags().String(flagClientOrderID, "", "optional client order id")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func placeMarketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "place-market-order [market-id] [buy|sell] [quantity-lots] [price-bound-ticks] [command-nonce]",
		Short: "Place a market order",
		Args:  cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			return broadcastOrder(cmd, args, v1.OrderType_ORDER_TYPE_MARKET, v1.TimeInForce_TIME_IN_FORCE_IOC)
		},
	}
	cmd.Flags().String(flagTimeInForce, "ioc", "ioc or fok")
	cmd.Flags().String(flagClientOrderID, "", "optional client order id")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func cancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel-order [order-id-hex] [command-nonce]",
		Short: "Cancel an open order",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			id, err := parseHex(args[0])
			if err != nil || len(id) != 32 {
				return fmt.Errorf("order id must be 32 bytes of hex")
			}
			nonce, err := parseU64(args[1])
			if err != nil {
				return err
			}
			msg := &v1.MsgCancelOrder{
				Owner:        clientCtx.GetFromAddress().String(),
				OrderId:      id,
				CommandNonce: nonce,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func broadcastOrder(cmd *cobra.Command, args []string, typ v1.OrderType, defaultTIF v1.TimeInForce) error {
	clientCtx, err := client.GetClientTxContext(cmd)
	if err != nil {
		return err
	}
	marketID, err := parseU64(args[0])
	if err != nil {
		return err
	}
	side, err := parseSide(args[1])
	if err != nil {
		return err
	}
	qty, err := parseU64(args[2])
	if err != nil {
		return err
	}
	price, err := parseU64(args[3])
	if err != nil {
		return err
	}
	nonce, err := parseU64(args[4])
	if err != nil {
		return err
	}
	tif := defaultTIF
	if cmd.Flags().Changed(flagTimeInForce) || typ == v1.OrderType_ORDER_TYPE_LIMIT {
		raw, err := cmd.Flags().GetString(flagTimeInForce)
		if err != nil {
			return err
		}
		tif, err = parseTIF(raw)
		if err != nil {
			return err
		}
	}
	var expiry uint64
	if cmd.Flags().Lookup(flagExpiry) != nil {
		expiry, err = cmd.Flags().GetUint64(flagExpiry)
		if err != nil {
			return err
		}
	}
	var clientID []byte
	if raw, err := cmd.Flags().GetString(flagClientOrderID); err != nil {
		return err
	} else if raw != "" {
		clientID = []byte(raw)
	}
	msg := &v1.MsgPlaceOrder{
		Owner:         clientCtx.GetFromAddress().String(),
		MarketId:      marketID,
		Side:          side,
		OrderType:     typ,
		TimeInForce:   tif,
		QuantityLots:  qty,
		PriceTicks:    price,
		ExpiryHeight:  expiry,
		CommandNonce:  nonce,
		ClientOrderId: clientID,
	}
	return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
}

func parseSide(s string) (v1.Side, error) {
	switch strings.ToLower(s) {
	case "buy":
		return v1.Side_SIDE_BUY, nil
	case "sell":
		return v1.Side_SIDE_SELL, nil
	default:
		return 0, fmt.Errorf("side must be buy or sell")
	}
}

func parseTIF(s string) (v1.TimeInForce, error) {
	switch strings.ToLower(s) {
	case "gtc":
		return v1.TimeInForce_TIME_IN_FORCE_GTC, nil
	case "ioc":
		return v1.TimeInForce_TIME_IN_FORCE_IOC, nil
	case "fok":
		return v1.TimeInForce_TIME_IN_FORCE_FOK, nil
	case "gtd":
		return v1.TimeInForce_TIME_IN_FORCE_GTD, nil
	default:
		return 0, fmt.Errorf("time in force must be gtc, ioc, fok, or gtd")
	}
}

func parseU64(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}
