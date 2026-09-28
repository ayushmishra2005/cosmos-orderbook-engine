package orderbook

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

// Deposit moves bank coins into exchange custody.
// amount is an atomic integer, not a decimal.
func (c *Client) Deposit(ctx context.Context, denom string, amount uint64) (TxResult, error) {
	signer, err := c.currentSigner()
	if err != nil {
		return TxResult{}, err
	}
	coin, err := atomCoin(denom, amount)
	if err != nil {
		return TxResult{}, err
	}
	return c.broadcast(ctx, signer, &exchangev1.MsgDeposit{Owner: signer.Address().String(), Amount: coin})
}

// Withdraw returns available atoms to the owner's bank account.
// amount is an atomic integer. Locked balance cannot be withdrawn.
func (c *Client) Withdraw(ctx context.Context, denom string, amount uint64) (TxResult, error) {
	signer, err := c.currentSigner()
	if err != nil {
		return TxResult{}, err
	}
	coin, err := atomCoin(denom, amount)
	if err != nil {
		return TxResult{}, err
	}
	return c.broadcast(ctx, signer, &exchangev1.MsgWithdraw{Owner: signer.Address().String(), Amount: coin})
}

// PlaceLimitOrder submits a limit order. The chain derives the order ID.
func (c *Client) PlaceLimitOrder(ctx context.Context, order LimitOrder) (TxResult, error) {
	signer, err := c.currentSigner()
	if err != nil {
		return TxResult{}, err
	}
	if err := checkPlace(order.MarketID, order.Side, order.TimeInForce, order.QuantityLots, order.PriceTicks, order.CommandNonce, order.ExpiryHeight, false); err != nil {
		return TxResult{}, err
	}
	msg := &exchangev1.MsgPlaceOrder{
		Owner: signer.Address().String(), MarketId: order.MarketID,
		Side: exchangev1.Side(order.Side), OrderType: exchangev1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:  exchangev1.TimeInForce(order.TimeInForce),
		QuantityLots: order.QuantityLots, PriceTicks: order.PriceTicks,
		ExpiryHeight: order.ExpiryHeight, CommandNonce: order.CommandNonce,
		ClientOrderId: append([]byte(nil), order.ClientOrderID...),
	}
	return c.broadcast(ctx, signer, msg)
}

// PlaceMarketOrder submits a market order.
// WorstPriceTicks is the worst acceptable tick and must be set by the caller.
func (c *Client) PlaceMarketOrder(ctx context.Context, order MarketOrder) (TxResult, error) {
	signer, err := c.currentSigner()
	if err != nil {
		return TxResult{}, err
	}
	if order.WorstPriceTicks == 0 {
		return TxResult{}, fmt.Errorf("%w: market order requires a worst price tick", ErrInvalidArgument)
	}
	if order.TimeInForce != domain.TimeInForceIOC && order.TimeInForce != domain.TimeInForceFOK {
		return TxResult{}, fmt.Errorf("%w: market order time in force", ErrInvalidArgument)
	}
	if err := checkPlace(order.MarketID, order.Side, order.TimeInForce, order.QuantityLots, order.WorstPriceTicks, order.CommandNonce, 0, true); err != nil {
		return TxResult{}, err
	}
	msg := &exchangev1.MsgPlaceOrder{
		Owner: signer.Address().String(), MarketId: order.MarketID,
		Side: exchangev1.Side(order.Side), OrderType: exchangev1.OrderType_ORDER_TYPE_MARKET,
		TimeInForce:  exchangev1.TimeInForce(order.TimeInForce),
		QuantityLots: order.QuantityLots, PriceTicks: order.WorstPriceTicks,
		CommandNonce:  order.CommandNonce,
		ClientOrderId: append([]byte(nil), order.ClientOrderID...),
	}
	return c.broadcast(ctx, signer, msg)
}

// CancelOrder cancels one live order by the chain-assigned ID.
func (c *Client) CancelOrder(ctx context.Context, cancel Cancel) (TxResult, error) {
	signer, err := c.currentSigner()
	if err != nil {
		return TxResult{}, err
	}
	if cancel.OrderID.IsZero() || cancel.CommandNonce == 0 {
		return TxResult{}, fmt.Errorf("%w: cancel", ErrInvalidArgument)
	}
	msg := &exchangev1.MsgCancelOrder{
		Owner: signer.Address().String(), OrderId: cancel.OrderID[:], CommandNonce: cancel.CommandNonce,
	}
	return c.broadcast(ctx, signer, msg)
}

// SignPlaceOrder returns an owner-signed command for the sequencer.
// The signature covers canonical batch-command bytes, the same bytes x/batch verifies.
func (c *Client) SignPlaceOrder(signer Signer, nonce uint64, body PlaceCommand) (canonical.Command, error) {
	if signer == nil {
		return canonical.Command{}, ErrSignerRequired
	}
	if nonce == 0 || body.MarketID == 0 || body.QuantityLots == 0 || body.PriceTicks == 0 {
		return canonical.Command{}, fmt.Errorf("%w: place command", ErrInvalidArgument)
	}
	if !body.Side.Valid() || !body.Type.Valid() || !body.TimeInForce.Valid() {
		return canonical.Command{}, fmt.Errorf("%w: place command", ErrInvalidArgument)
	}
	cmd := canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            c.chainID,
		ExchangeInstanceID: append([]byte(nil), c.instance...),
		Owner:              append([]byte(nil), signer.Address()...),
		Nonce:              nonce,
		Type:               canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID: domain.MarketID(body.MarketID), Side: body.Side, Type: body.Type,
			TimeInForce: body.TimeInForce, Quantity: domain.Quantity(body.QuantityLots),
			Price: domain.Price(body.PriceTicks), ExpiryHeight: body.ExpiryHeight,
			ClientOrderID: append([]byte(nil), body.ClientOrderID...),
		},
		PubKey: signer.PubKey().Bytes(),
	}
	return c.signCommand(signer, cmd)
}

// SignCancelOrder returns an owner-signed cancel command.
// The signature covers canonical batch-command bytes, the same bytes x/batch verifies.
func (c *Client) SignCancelOrder(signer Signer, nonce uint64, id domain.OrderID) (canonical.Command, error) {
	if signer == nil {
		return canonical.Command{}, ErrSignerRequired
	}
	if nonce == 0 || id.IsZero() {
		return canonical.Command{}, fmt.Errorf("%w: cancel command", ErrInvalidArgument)
	}
	cmd := canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            c.chainID,
		ExchangeInstanceID: append([]byte(nil), c.instance...),
		Owner:              append([]byte(nil), signer.Address()...),
		Nonce:              nonce,
		Type:               canonical.CommandTypeCancel,
		Cancel:             &canonical.Cancel{OrderID: id},
		PubKey:             signer.PubKey().Bytes(),
	}
	return c.signCommand(signer, cmd)
}

func (c *Client) signCommand(signer Signer, cmd canonical.Command) (canonical.Command, error) {
	bz, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		return canonical.Command{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	sig, err := signer.Sign(bz)
	if err != nil {
		return canonical.Command{}, err
	}
	if len(sig) != 64 {
		return canonical.Command{}, fmt.Errorf("%w: signature length", ErrInvalidArgument)
	}
	pub := signer.PubKey().Bytes()
	if len(pub) != secp256k1.PubKeySize {
		return canonical.Command{}, fmt.Errorf("%w: public key", ErrInvalidArgument)
	}
	cmd.PubKey = append([]byte(nil), pub...)
	cmd.Signature = sig
	return cmd, nil
}

func checkPlace(market uint64, side domain.Side, tif domain.TimeInForce, qty, price, nonce, expiry uint64, marketOrder bool) error {
	if market == 0 || qty == 0 || price == 0 || nonce == 0 || !side.Valid() || !tif.Valid() {
		return fmt.Errorf("%w: order", ErrInvalidArgument)
	}
	if tif == domain.TimeInForceGTD && expiry == 0 {
		return fmt.Errorf("%w: gtd expiry", ErrInvalidArgument)
	}
	if tif != domain.TimeInForceGTD && expiry != 0 && !marketOrder {
		return fmt.Errorf("%w: expiry", ErrInvalidArgument)
	}
	return nil
}

func atomCoin(denom string, amount uint64) (sdk.Coin, error) {
	if amount == 0 || denom == "" {
		return sdk.Coin{}, fmt.Errorf("%w: amount", ErrInvalidArgument)
	}
	coin := sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount))
	if err := coin.Validate(); err != nil {
		return sdk.Coin{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	return coin, nil
}

func (c *Client) broadcast(ctx context.Context, signer Signer, msg sdk.Msg) (TxResult, error) {
	c.txMu.Lock()
	defer c.txMu.Unlock()
	acc, err := c.account(ctx, signer.Address().String())
	if err != nil {
		return TxResult{}, err
	}
	builder := c.txConfig.NewTxBuilder()
	if err := builder.SetMsgs(msg); err != nil {
		return TxResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	builder.SetGasLimit(c.gas)
	if c.fees != "" {
		fee, err := sdk.ParseCoinsNormalized(c.fees)
		if err != nil {
			return TxResult{}, fmt.Errorf("%w: fees", ErrInvalidArgument)
		}
		builder.SetFeeAmount(fee)
	}
	if err := signTx(ctx, c.txConfig, signer, c.chainID, acc.GetAccountNumber(), acc.GetSequence(), builder); err != nil {
		return TxResult{}, err
	}
	txBytes, err := c.txConfig.TxEncoder()(builder.GetTx())
	if err != nil {
		return TxResult{}, fmt.Errorf("%w: encode", ErrTxRejected)
	}
	res, err := c.rpc.BroadcastTxSync(ctx, txBytes)
	if err != nil {
		return TxResult{}, fmt.Errorf("%w: %v", ErrRPCUnavailable, err)
	}
	if res.Code != 0 {
		return TxResult{}, fmt.Errorf("%w: checktx code %d: %s", ErrTxRejected, res.Code, res.Log)
	}
	hash := append([]byte(nil), res.Hash...)
	if len(hash) == 0 {
		return TxResult{}, fmt.Errorf("%w: broadcast hash", ErrTxRejected)
	}
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	tick := time.NewTimer(200 * time.Millisecond)
	defer tick.Stop()
	for {
		result, err := c.rpc.Tx(ctx, hash, false)
		if err == nil {
			if result.TxResult.Code != 0 {
				return TxResult{}, fmt.Errorf("%w: code %d: %s", ErrTxRejected, result.TxResult.Code, result.TxResult.Log)
			}
			out := TxResult{
				Hash:   strings.ToUpper(hex.EncodeToString(hash)),
				Height: result.Height,
				Code:   result.TxResult.Code,
				Log:    result.TxResult.Log,
			}
			fillResponse(&out, result.TxResult.Data)
			return out, nil
		}
		select {
		case <-ctx.Done():
			return TxResult{}, ctx.Err()
		case <-deadline.C:
			return TxResult{}, fmt.Errorf("%w: timed out waiting for inclusion", ErrRPCUnavailable)
		case <-tick.C:
			tick.Reset(200 * time.Millisecond)
		}
	}
}

func (c *Client) account(ctx context.Context, bech32 string) (sdk.AccountI, error) {
	res, err := authtypes.NewQueryClient(c.grpcConn).Account(ctx, &authtypes.QueryAccountRequest{Address: bech32})
	if err != nil {
		return nil, mapQuery(err)
	}
	var acc sdk.AccountI
	if err := c.registry.UnpackAny(res.Account, &acc); err != nil {
		return nil, fmt.Errorf("%w: account", ErrGRPCUnavailable)
	}
	return acc, nil
}

func signTx(ctx context.Context, txConfig client.TxConfig, signer Signer, chainID string, accNum, seq uint64, builder client.TxBuilder) error {
	mode := signingtypes.SignMode_SIGN_MODE_DIRECT
	sig := signingtypes.SignatureV2{
		PubKey: signer.PubKey(),
		Data: &signingtypes.SingleSignatureData{
			SignMode: mode,
		},
		Sequence: seq,
	}
	if err := builder.SetSignatures(sig); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	bytesToSign, err := authsigning.GetSignBytesAdapter(ctx, txConfig.SignModeHandler(), mode, authsigning.SignerData{
		Address:       signer.Address().String(),
		ChainID:       chainID,
		AccountNumber: accNum,
		Sequence:      seq,
		PubKey:        signer.PubKey(),
	}, builder.GetTx())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	sigBytes, err := signer.Sign(bytesToSign)
	if err != nil {
		return err
	}
	sig.Data = &signingtypes.SingleSignatureData{SignMode: mode, Signature: sigBytes}
	if err := builder.SetSignatures(sig); err != nil {
		return fmt.Errorf("%w: %v", ErrTxRejected, err)
	}
	return nil
}

func fillResponse(out *TxResult, data []byte) {
	if len(data) == 0 {
		return
	}
	var body sdk.TxMsgData
	if err := body.Unmarshal(data); err != nil {
		return
	}
	for _, any := range body.MsgResponses {
		if any == nil {
			continue
		}
		switch {
		case strings.Contains(any.TypeUrl, "MsgPlaceOrderResponse"):
			var resp exchangev1.MsgPlaceOrderResponse
			if err := resp.Unmarshal(any.Value); err != nil {
				continue
			}
			if len(resp.OrderId) == len(out.OrderID) {
				copy(out.OrderID[:], resp.OrderId)
			}
			out.RemainingLots = resp.RemainingLots
			out.Rested = resp.Rested
		case strings.Contains(any.TypeUrl, "MsgCancelOrderResponse"):
			var resp exchangev1.MsgCancelOrderResponse
			if err := resp.Unmarshal(any.Value); err != nil {
				continue
			}
			if len(resp.OrderId) == len(out.OrderID) {
				copy(out.OrderID[:], resp.OrderId)
			}
			out.AssetID = resp.AssetId
			out.Released = resp.Released
		}
	}
}
