package keeper

import (
	"bytes"
	"context"
	"errors"
	"time"

	"cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/internal/telemetry"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/arithmetic"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

// errInjected aborts a batch in tests. Production leaves the hooks unset.
var errInjected = errors.New("batch: injected failure")

// Exchange is the exchange surface a batch may call.
// The batch does not match, reserve, or settle.
type Exchange interface {
	InstanceID() []byte
	GetRevision(ctx context.Context) (uint64, error)
	PlaceOrder(ctx context.Context, cmd exchangetypes.PlaceOrderCommand) (exchangetypes.PlaceResult, error)
	CancelOrder(ctx context.Context, cmd exchangetypes.CancelOrderCommand) (exchangetypes.CancelResult, error)
}

// Keeper executes ordered batches against the exchange.
//
// failAfterCommands, failBeforeRecord, and failBeforeWrite are test hooks.
// Production leaves them unset. A hit returns before the batch cache is written.
type Keeper struct {
	store             store.KVStoreService
	chainID           string
	ex                Exchange
	failAfterCommands int
	failBeforeRecord  bool
	failBeforeWrite   error
}

// NewKeeper binds the batch store. chainID is the chain the commands must name.
func NewKeeper(svc store.KVStoreService, chainID string, ex Exchange) (Keeper, error) {
	if svc == nil || ex == nil {
		return Keeper{}, types.ErrCorrupt
	}
	if _, err := canonical.CommandSignBytes(canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            chainID,
		ExchangeInstanceID: ex.InstanceID(),
		Owner:              []byte{1},
		Nonce:              1,
		Type:               canonical.CommandTypeCancel,
		Cancel:             &canonical.Cancel{OrderID: domain.OrderID{1}},
	}); err != nil {
		return Keeper{}, err
	}
	return Keeper{store: svc, chainID: chainID, ex: ex}, nil
}

// FinalizeBatch checks the batch, then runs every command in order inside one cache.
// The results hash, commitment, and head are written only after every command succeeds.
// A failure discards the cache.
func (k Keeper) FinalizeBatch(ctx context.Context, msg *v1.MsgFinalizeBatch) (types.Batch, error) {
	start := time.Now()
	var out types.Batch
	err := k.commit(ctx, func(ctx sdk.Context) error {
		batch, err := k.finalize(ctx, msg)
		if err != nil {
			return err
		}
		out = batch
		return nil
	})
	if err != nil {
		return types.Batch{}, err
	}
	telemetry.RecordBatch(len(out.Results), time.Since(start))
	return out, nil
}

func (k Keeper) commit(ctx context.Context, fn func(sdk.Context) error) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := fn(cacheCtx); err != nil {
		return err
	}
	if k.failBeforeWrite != nil {
		return k.failBeforeWrite
	}
	write()
	return nil
}

func (k Keeper) finalize(ctx sdk.Context, msg *v1.MsgFinalizeBatch) (types.Batch, error) {
	if msg == nil {
		return types.Batch{}, types.ErrEmpty
	}
	submitter, err := sdk.AccAddressFromBech32(msg.Submitter)
	if err != nil {
		return types.Batch{}, err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return types.Batch{}, err
	}
	if !bytes.Equal(params.Submitter, submitter) {
		return types.Batch{}, types.ErrUnauthorized
	}
	next, err := arithmetic.Add(params.Latest, 1)
	if err != nil {
		return types.Batch{}, err
	}
	if msg.BatchNumber != next {
		return types.Batch{}, types.ErrBatchNumber
	}
	pre, err := k.ex.GetRevision(ctx)
	if err != nil {
		return types.Batch{}, err
	}
	if pre != msg.ExpectedExchangeRevision {
		return types.Batch{}, types.ErrStaleRevision
	}
	if len(msg.Commands) == 0 {
		return types.Batch{}, types.ErrEmpty
	}
	if len(msg.Commands) > types.MaxCommands {
		return types.Batch{}, types.ErrLimit
	}
	submitted, err := previousCommitment(msg.PreviousBatchCommitment)
	if err != nil {
		return types.Batch{}, err
	}
	if submitted != params.Head {
		return types.Batch{}, types.ErrPreviousCommitment
	}
	commands := make([]canonical.Command, len(msg.Commands))
	for i, pb := range msg.Commands {
		cmd, err := commandFromProto(pb)
		if err != nil {
			return types.Batch{}, err
		}
		if err := verifyCommand(cmd, k.chainID, k.ex.InstanceID()); err != nil {
			return types.Batch{}, err
		}
		commands[i] = cmd
	}
	height, err := executionHeight(ctx)
	if err != nil {
		return types.Batch{}, err
	}
	results := make([]types.CommandResult, len(commands))
	for i := range commands {
		result, err := k.execute(ctx, commands[i])
		if err != nil {
			return types.Batch{}, &types.CommandRejection{Index: i, Err: err}
		}
		result.Index = uint32(i)
		results[i] = result
		if k.failAfterCommands > 0 && i+1 == k.failAfterCommands {
			return types.Batch{}, errInjected
		}
	}
	post, err := k.ex.GetRevision(ctx)
	if err != nil {
		return types.Batch{}, err
	}
	want, err := arithmetic.Add(pre, uint64(len(results)))
	if err != nil || want != post {
		return types.Batch{}, types.ErrInvariant
	}
	rawID, err := canonical.HashBatchID(msg.BatchNumber, pre, commands)
	if err != nil {
		return types.Batch{}, err
	}
	batch := types.Batch{
		Number:       msg.BatchNumber,
		ID:           types.BatchID(rawID),
		Height:       height,
		PreRevision:  pre,
		PostRevision: post,
		Previous:     submitted,
		Results:      results,
	}
	if err := batch.Validate(); err != nil {
		return types.Batch{}, err
	}
	resultsHash, commitment, err := k.batchHashes(batch)
	if err != nil {
		return types.Batch{}, err
	}
	batch.ResultsHash = resultsHash
	batch.Commitment = commitment
	if k.failBeforeRecord {
		return types.Batch{}, errInjected
	}
	if err := k.storeBatch(ctx, batch); err != nil {
		return types.Batch{}, err
	}
	params.Latest = batch.Number
	params.Head = batch.Commitment
	if err := k.setParams(ctx, params); err != nil {
		return types.Batch{}, err
	}
	emitFinalized(ctx, batch)
	return batch, nil
}

func (k Keeper) execute(ctx sdk.Context, cmd canonical.Command) (types.CommandResult, error) {
	switch cmd.Type {
	case canonical.CommandTypePlace:
		res, err := k.ex.PlaceOrder(ctx, exchangetypes.PlaceOrderCommand{
			Owner:         append([]byte(nil), cmd.Owner...),
			MarketID:      cmd.Place.MarketID,
			Side:          cmd.Place.Side,
			Type:          cmd.Place.Type,
			TimeInForce:   cmd.Place.TimeInForce,
			Price:         cmd.Place.Price,
			Quantity:      cmd.Place.Quantity,
			ExpiryHeight:  cmd.Place.ExpiryHeight,
			CommandNonce:  cmd.Nonce,
			ClientOrderID: append([]byte(nil), cmd.Place.ClientOrderID...),
		})
		if err != nil {
			return types.CommandResult{}, err
		}
		trades := make([]types.TradeRef, len(res.Fills))
		for i, fill := range res.Fills {
			trades[i] = types.TradeRef{MarketID: fill.MarketID, Sequence: fill.Sequence}
		}
		return types.CommandResult{
			Type:      types.CommandPlace,
			Owner:     append([]byte(nil), cmd.Owner...),
			OrderID:   res.OrderID,
			Status:    placeStatus(res),
			Remaining: uint64(res.Remaining),
			Trades:    trades,
		}, nil
	case canonical.CommandTypeCancel:
		res, err := k.ex.CancelOrder(ctx, exchangetypes.CancelOrderCommand{
			Owner:        append([]byte(nil), cmd.Owner...),
			OrderID:      cmd.Cancel.OrderID,
			CommandNonce: cmd.Nonce,
		})
		if err != nil {
			return types.CommandResult{}, err
		}
		return types.CommandResult{
			Type:    types.CommandCancel,
			Owner:   append([]byte(nil), cmd.Owner...),
			OrderID: res.OrderID,
			Status:  types.StatusCancelled,
		}, nil
	default:
		return types.CommandResult{}, canonical.ErrInvalidCommand
	}
}

func placeStatus(res exchangetypes.PlaceResult) byte {
	if res.Rested {
		return types.StatusResting
	}
	if res.Remaining == 0 {
		return types.StatusFilled
	}
	return types.StatusUnfilled
}

func verifyCommand(cmd canonical.Command, chainID string, instance []byte) error {
	if _, err := canonical.CommandSignBytes(cmd); err != nil {
		return err
	}
	if cmd.ChainID != chainID {
		return types.ErrChainID
	}
	if !bytes.Equal(cmd.ExchangeInstanceID, instance) {
		return types.ErrInstance
	}
	if len(cmd.PubKey) != secp256k1.PubKeySize || (cmd.PubKey[0] != 0x02 && cmd.PubKey[0] != 0x03) {
		return types.ErrPubKey
	}
	if len(cmd.Signature) != 64 {
		return types.ErrInvalidSignature
	}
	sign, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		return err
	}
	pub := &secp256k1.PubKey{Key: append([]byte(nil), cmd.PubKey...)}
	if !pub.VerifySignature(sign, cmd.Signature) {
		return types.ErrInvalidSignature
	}
	if !bytes.Equal([]byte(sdk.AccAddress(pub.Address())), cmd.Owner) {
		return types.ErrPubKeyMismatch
	}
	return nil
}

func commandFromProto(msg *v1.SignedCommand) (canonical.Command, error) {
	if msg == nil {
		return canonical.Command{}, canonical.ErrInvalidCommand
	}
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return canonical.Command{}, err
	}
	cmd := canonical.Command{
		ProtocolVersion:    msg.ProtocolVersion,
		ChainID:            msg.ChainId,
		ExchangeInstanceID: append([]byte(nil), msg.ExchangeInstanceId...),
		Owner:              append([]byte(nil), owner...),
		Nonce:              msg.CommandNonce,
		PubKey:             append([]byte(nil), msg.PubKey...),
		Signature:          append([]byte(nil), msg.Signature...),
	}
	switch msg.CommandType {
	case v1.CommandType_COMMAND_TYPE_PLACE_ORDER:
		if msg.Place == nil || msg.Cancel != nil {
			return canonical.Command{}, canonical.ErrInvalidCommand
		}
		side, err := asSide(msg.Place.Side)
		if err != nil {
			return canonical.Command{}, err
		}
		typ, err := asOrderType(msg.Place.OrderType)
		if err != nil {
			return canonical.Command{}, err
		}
		tif, err := asTimeInForce(msg.Place.TimeInForce)
		if err != nil {
			return canonical.Command{}, err
		}
		cmd.Type = canonical.CommandTypePlace
		cmd.Place = &canonical.Place{
			MarketID:      domain.MarketID(msg.Place.MarketId),
			Side:          side,
			Type:          typ,
			TimeInForce:   tif,
			Quantity:      domain.Quantity(msg.Place.QuantityLots),
			Price:         domain.Price(msg.Place.PriceTicks),
			ExpiryHeight:  msg.Place.ExpiryHeight,
			ClientOrderID: append([]byte(nil), msg.Place.ClientOrderId...),
		}
	case v1.CommandType_COMMAND_TYPE_CANCEL_ORDER:
		if msg.Cancel == nil || msg.Place != nil || len(msg.Cancel.OrderId) != len(domain.OrderID{}) {
			return canonical.Command{}, canonical.ErrInvalidCommand
		}
		var id domain.OrderID
		copy(id[:], msg.Cancel.OrderId)
		cmd.Type = canonical.CommandTypeCancel
		cmd.Cancel = &canonical.Cancel{OrderID: id}
	default:
		return canonical.Command{}, canonical.ErrInvalidCommand
	}
	return cmd, nil
}

func asSide(v v1.Side) (domain.Side, error) {
	switch v {
	case v1.Side_SIDE_BUY:
		return domain.SideBuy, nil
	case v1.Side_SIDE_SELL:
		return domain.SideSell, nil
	default:
		return 0, domain.ErrInvalidSide
	}
}

func asOrderType(v v1.OrderType) (domain.OrderType, error) {
	switch v {
	case v1.OrderType_ORDER_TYPE_LIMIT:
		return domain.OrderTypeLimit, nil
	case v1.OrderType_ORDER_TYPE_MARKET:
		return domain.OrderTypeMarket, nil
	default:
		return 0, domain.ErrInvalidOrderType
	}
}

func asTimeInForce(v v1.TimeInForce) (domain.TimeInForce, error) {
	switch v {
	case v1.TimeInForce_TIME_IN_FORCE_GTC:
		return domain.TimeInForceGTC, nil
	case v1.TimeInForce_TIME_IN_FORCE_IOC:
		return domain.TimeInForceIOC, nil
	case v1.TimeInForce_TIME_IN_FORCE_FOK:
		return domain.TimeInForceFOK, nil
	case v1.TimeInForce_TIME_IN_FORCE_GTD:
		return domain.TimeInForceGTD, nil
	default:
		return 0, domain.ErrInvalidTimeInForce
	}
}

func executionHeight(ctx sdk.Context) (uint64, error) {
	height := ctx.BlockHeight()
	if height < 0 {
		return 0, types.ErrNegativeHeight
	}
	return uint64(height), nil
}

func (k Keeper) batchHashes(batch types.Batch) (types.ResultsHash, types.BatchCommitment, error) {
	resultsHash, err := canonical.HashResults(canonicalResults(batch.Results))
	if err != nil {
		return types.ResultsHash{}, types.BatchCommitment{}, err
	}
	commitment, err := canonical.HashBatchCommitment(canonical.BatchCommitmentInput{
		Version:                 canonical.BatchCommitmentVersion,
		ChainID:                 k.chainID,
		ExchangeInstanceID:      k.ex.InstanceID(),
		BatchNumber:             batch.Number,
		BatchID:                 [32]byte(batch.ID),
		PreviousBatchCommitment: [32]byte(batch.Previous),
		ExecutionHeight:         batch.Height,
		PreExchangeRevision:     batch.PreRevision,
		PostExchangeRevision:    batch.PostRevision,
		ResultsHash:             resultsHash,
	})
	if err != nil {
		return types.ResultsHash{}, types.BatchCommitment{}, err
	}
	return types.ResultsHash(resultsHash), types.BatchCommitment(commitment), nil
}

func (k Keeper) verifyCommitment(batch types.Batch) error {
	resultsHash, commitment, err := k.batchHashes(batch)
	if err != nil {
		return err
	}
	if resultsHash != batch.ResultsHash || commitment != batch.Commitment {
		return types.ErrCorrupt
	}
	return nil
}

func canonicalResults(results []types.CommandResult) []canonical.Result {
	out := make([]canonical.Result, len(results))
	for i, result := range results {
		trades := make([]canonical.ResultTrade, len(result.Trades))
		for j, trade := range result.Trades {
			trades[j] = canonical.ResultTrade{MarketID: trade.MarketID, Sequence: trade.Sequence}
		}
		out[i] = canonical.Result{
			Index:     result.Index,
			Type:      canonical.CommandType(result.Type),
			Owner:     result.Owner,
			OrderID:   result.OrderID,
			Status:    result.Status,
			Remaining: result.Remaining,
			Trades:    trades,
		}
	}
	return out
}

func previousCommitment(bz []byte) (types.BatchCommitment, error) {
	if len(bz) != len(types.BatchCommitment{}) {
		return types.BatchCommitment{}, types.ErrPreviousCommitment
	}
	var out types.BatchCommitment
	copy(out[:], bz)
	return out, nil
}

func emitFinalized(ctx sdk.Context, batch types.Batch) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeBatchFinalized,
		sdk.NewAttribute("batch_number", u64(batch.Number)),
		sdk.NewAttribute("batch_id", hexEncoded(batch.ID[:])),
		sdk.NewAttribute("batch_commitment", hexEncoded(batch.Commitment[:])),
		sdk.NewAttribute("results_hash", hexEncoded(batch.ResultsHash[:])),
		sdk.NewAttribute("command_count", u64(uint64(len(batch.Results)))),
		sdk.NewAttribute("pre_revision", u64(batch.PreRevision)),
		sdk.NewAttribute("post_revision", u64(batch.PostRevision)),
	))
}

func hexEncoded(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

func u64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
