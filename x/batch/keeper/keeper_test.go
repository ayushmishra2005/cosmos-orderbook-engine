package keeper

import (
	"bytes"
	"errors"
	"testing"

	"cosmossdk.io/log/v2"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/store/v2"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	v1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types/v1"
	exchangekeeper "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/keeper"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

const (
	chainID    = "testing"
	baseAsset  = domain.AssetID(1)
	quoteAsset = domain.AssetID(2)
	mkt        = domain.MarketID(1)
)

type trader struct {
	priv *secp256k1.PrivKey
	addr sdk.AccAddress
}

func newTrader() trader {
	priv := secp256k1.GenPrivKey()
	return trader{priv: priv, addr: sdk.AccAddress(priv.PubKey().Address())}
}

type env struct {
	ek       exchangekeeper.Keeper
	bk       Keeper
	ctx      sdk.Context
	exKey    *storetypes.KVStoreKey
	sub      trader
	instance []byte
}

func setup(t *testing.T) env {
	t.Helper()
	db := dbm.NewMemDB()
	cms := store.NewCommitMultiStore(db, log.NewNopLogger())
	exKey := storetypes.NewKVStoreKey("exchange")
	batchKey := storetypes.NewKVStoreKey("batch")
	tkey := storetypes.NewTransientStoreKey("transient")
	cms.MountStoreWithDB(exKey, storetypes.StoreTypeIAVL, db)
	cms.MountStoreWithDB(batchKey, storetypes.StoreTypeIAVL, db)
	cms.MountStoreWithDB(tkey, storetypes.StoreTypeTransient, db)
	if err := cms.LoadLatestVersion(); err != nil {
		t.Fatal(err)
	}
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 10, ChainID: chainID}, false, log.NewNopLogger())
	ek, err := exchangekeeper.NewKeeper(runtime.NewKVStoreService(exKey), chainID, []byte{0, 0, 0, 0, 0, 0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	bk, err := NewKeeper(runtime.NewKVStoreService(batchKey), chainID, ek)
	if err != nil {
		t.Fatal(err)
	}
	sub := newTrader()
	if err := bk.InitGenesis(ctx, v1.GenesisState{Submitter: sub.addr.String()}); err != nil {
		t.Fatal(err)
	}
	if err := ek.CreateMarket(ctx, exchangetypes.Market{
		ID: mkt, BaseAssetID: baseAsset, QuoteAssetID: quoteAsset,
		BaseLotSize: 1, QuoteAtomsPerTickPerLot: 1, MaxMakerVisits: 64, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return env{ek: ek, bk: bk, ctx: ctx, exKey: exKey, sub: sub, instance: ek.InstanceID()}
}

func (e env) fund(t *testing.T, owner []byte, asset domain.AssetID, amount uint64) {
	t.Helper()
	key, err := canonical.EncodeBalanceKey(owner, asset)
	if err != nil {
		t.Fatal(err)
	}
	kv := runtime.NewKVStoreService(e.exKey).OpenKVStore(e.ctx)
	if err := kv.Set(key, exchangetypes.EncodeBalance(exchangetypes.Balance{Available: amount})); err != nil {
		t.Fatal(err)
	}
}

func (e env) revision(t *testing.T) uint64 {
	t.Helper()
	rev, err := e.ek.GetRevision(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func (e env) nonce(t *testing.T, owner []byte) uint64 {
	t.Helper()
	n, err := e.ek.GetCommandNonce(e.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e env) bal(t *testing.T, owner []byte, asset domain.AssetID) exchangetypes.Balance {
	t.Helper()
	got, err := e.ek.GetBalance(e.ctx, owner, asset)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e env) msg(number, rev uint64, cmds ...*v1.SignedCommand) *v1.MsgFinalizeBatch {
	params, err := e.bk.GetParams(e.ctx)
	if err != nil {
		panic(err)
	}
	return &v1.MsgFinalizeBatch{
		Submitter:                e.sub.addr.String(),
		BatchNumber:              number,
		ExpectedExchangeRevision: rev,
		Commands:                 cmds,
		PreviousBatchCommitment:  append([]byte(nil), params.Head[:]...),
	}
}

func (e env) place(t *testing.T, tr trader, nonce uint64, side domain.Side, typ domain.OrderType, tif domain.TimeInForce, price, qty uint64) (*v1.SignedCommand, canonical.Command) {
	t.Helper()
	return e.command(t, tr, canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            chainID,
		ExchangeInstanceID: append([]byte(nil), e.instance...),
		Owner:              tr.addr,
		Nonce:              nonce,
		Type:               canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID: mkt, Side: side, Type: typ, TimeInForce: tif,
			Quantity: domain.Quantity(qty), Price: domain.Price(price),
		},
	})
}

func (e env) cancel(t *testing.T, tr trader, nonce uint64, id domain.OrderID) (*v1.SignedCommand, canonical.Command) {
	t.Helper()
	return e.command(t, tr, canonical.Command{
		ProtocolVersion:    canonical.BatchCommandVersion,
		ChainID:            chainID,
		ExchangeInstanceID: append([]byte(nil), e.instance...),
		Owner:              tr.addr,
		Nonce:              nonce,
		Type:               canonical.CommandTypeCancel,
		Cancel:             &canonical.Cancel{OrderID: id},
	})
}

func (e env) command(t *testing.T, tr trader, cmd canonical.Command) (*v1.SignedCommand, canonical.Command) {
	t.Helper()
	cmd.PubKey = tr.priv.PubKey().Bytes()
	bz, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := tr.priv.Sign(bz)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Signature = sig
	pb := &v1.SignedCommand{
		ProtocolVersion:    cmd.ProtocolVersion,
		ChainId:            cmd.ChainID,
		ExchangeInstanceId: append([]byte(nil), cmd.ExchangeInstanceID...),
		Owner:              sdk.AccAddress(cmd.Owner).String(),
		CommandNonce:       cmd.Nonce,
		PubKey:             append([]byte(nil), cmd.PubKey...),
		Signature:          append([]byte(nil), sig...),
	}
	switch cmd.Type {
	case canonical.CommandTypePlace:
		pb.CommandType = v1.CommandType_COMMAND_TYPE_PLACE_ORDER
		pb.Place = &v1.Place{
			MarketId: uint64(cmd.Place.MarketID), Side: sideProto(cmd.Place.Side),
			OrderType: typeProto(cmd.Place.Type), TimeInForce: tifProto(cmd.Place.TimeInForce),
			QuantityLots: uint64(cmd.Place.Quantity), PriceTicks: uint64(cmd.Place.Price),
			ExpiryHeight: cmd.Place.ExpiryHeight, ClientOrderId: append([]byte(nil), cmd.Place.ClientOrderID...),
		}
	case canonical.CommandTypeCancel:
		pb.CommandType = v1.CommandType_COMMAND_TYPE_CANCEL_ORDER
		pb.Cancel = &v1.Cancel{OrderId: append([]byte(nil), cmd.Cancel.OrderID[:]...)}
	default:
		t.Fatal("command type")
	}
	return pb, cmd
}

func sideProto(s domain.Side) v1.Side {
	if s == domain.SideSell {
		return v1.Side_SIDE_SELL
	}
	return v1.Side_SIDE_BUY
}

func typeProto(t domain.OrderType) v1.OrderType {
	if t == domain.OrderTypeMarket {
		return v1.OrderType_ORDER_TYPE_MARKET
	}
	return v1.OrderType_ORDER_TYPE_LIMIT
}

func tifProto(t domain.TimeInForce) v1.TimeInForce {
	switch t {
	case domain.TimeInForceIOC:
		return v1.TimeInForce_TIME_IN_FORCE_IOC
	case domain.TimeInForceFOK:
		return v1.TimeInForce_TIME_IN_FORCE_FOK
	case domain.TimeInForceGTD:
		return v1.TimeInForce_TIME_IN_FORCE_GTD
	default:
		return v1.TimeInForce_TIME_IN_FORCE_GTC
	}
}

func (e env) orderID(t *testing.T, owner []byte, nonce uint64) domain.OrderID {
	t.Helper()
	id, err := canonical.HashOrderID(canonical.OrderIDInput{
		ChainID: chainID, ExchangeInstanceID: e.instance, Owner: owner, MarketID: mkt, CommandNonce: nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e env) noOrder(t *testing.T, id domain.OrderID) {
	t.Helper()
	_, err := e.ek.GetOrder(e.ctx, id)
	if !errors.Is(err, exchangetypes.ErrNotFound) {
		t.Fatalf("order present: %v", err)
	}
}

func (e env) noBatch(t *testing.T) {
	t.Helper()
	if _, err := e.bk.LatestBatch(e.ctx); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	params, err := e.bk.GetParams(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if params.Latest != 0 || !params.Head.IsZero() {
		t.Fatalf("head latest=%d commitment=%x", params.Latest, params.Head)
	}
	if _, err := e.bk.GetBatch(e.ctx, 1); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
}

func (e env) head(t *testing.T) types.BatchCommitment {
	t.Helper()
	params, err := e.bk.GetParams(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return params.Head
}

func TestFirstBatch(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 20)
	if e.revision(t) != 1 {
		t.Fatalf("revision %d", e.revision(t))
	}
	pb, cmd := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 5)
	e.ctx = e.ctx.WithEventManager(sdk.NewEventManager())
	batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, pb))
	if err != nil {
		t.Fatal(err)
	}
	if batch.Number != 1 || batch.PreRevision != 1 || batch.PostRevision != 2 || batch.Height != 10 || len(batch.Results) != 1 {
		t.Fatalf("%+v", batch)
	}
	id, err := canonical.HashBatchID(1, 1, []canonical.Command{cmd})
	if err != nil || types.BatchID(id) != batch.ID {
		t.Fatalf("batch id %x %v", id, err)
	}
	again, err := canonical.HashBatchID(1, 1, []canonical.Command{cmd})
	if err != nil || again != id {
		t.Fatal("identical input changed the batch id")
	}
	stored, err := e.ek.GetOrder(e.ctx, batch.Results[0].OrderID)
	if err != nil || stored.Order.RemainingQuantity != 5 || stored.Order.Sequence != 1 {
		t.Fatalf("%+v %v", stored, err)
	}
	if e.nonce(t, alice.addr) != 1 || e.bal(t, alice.addr, baseAsset) != (exchangetypes.Balance{Available: 15, Locked: 5}) {
		t.Fatalf("nonce %d bal %+v", e.nonce(t, alice.addr), e.bal(t, alice.addr, baseAsset))
	}
	if !hasEvent(e.ctx, types.EventTypeBatchFinalized) {
		t.Fatal("missing batch_finalized")
	}
	qs := NewQueryServer(e.bk)
	byNum, err := qs.Batch(e.ctx, &v1.QueryBatchRequest{BatchNumber: 1})
	if err != nil || byNum.Batch.BatchNumber != 1 || !bytes.Equal(byNum.Batch.BatchId, batch.ID[:]) {
		t.Fatal(err)
	}
	latest, err := qs.LatestBatch(e.ctx, &v1.QueryLatestBatchRequest{})
	if err != nil || latest.Batch.BatchNumber != 1 {
		t.Fatal(err)
	}
	byID, err := qs.BatchByID(e.ctx, &v1.QueryBatchByIDRequest{BatchId: batch.ID[:]})
	if err != nil || byID.Batch.PostExchangeRevision != 2 {
		t.Fatal(err)
	}

	exported, err := e.bk.ExportGenesis(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := setup(t)
	if err := other.bk.InitGenesis(other.ctx, exported); err != nil {
		t.Fatal(err)
	}
	got, err := other.bk.GetBatch(other.ctx, 1)
	if err != nil || got.ID != batch.ID || got.Results[0].OrderID != batch.Results[0].OrderID {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Previous != batch.Previous || got.Commitment != batch.Commitment || got.ResultsHash != batch.ResultsHash {
		t.Fatalf("imported commitment %+v", got)
	}
	if !batch.Previous.IsZero() || e.head(t) != batch.Commitment {
		t.Fatalf("head %x commitment %x", e.head(t), batch.Commitment)
	}
}

func TestCommandsExecuteInOrder(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 20)
	e.fund(t, bob.addr, quoteAsset, 1000)
	sell9, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 9, 4)
	sell10, _ := e.place(t, alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	buy, _ := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 6)
	batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), sell9, sell10, buy))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 3 || len(batch.Results[2].Trades) != 2 {
		t.Fatalf("%+v", batch.Results)
	}
	if batch.Results[2].Trades[0].Sequence != 1 || batch.Results[2].Trades[1].Sequence != 2 {
		t.Fatalf("%+v", batch.Results[2].Trades)
	}
	first, err := e.ek.GetTrade(e.ctx, mkt, 1)
	if err != nil || first.Price != 9 || first.Quantity != 4 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := e.ek.GetTrade(e.ctx, mkt, 2)
	if err != nil || second.Price != 10 || second.Quantity != 2 {
		t.Fatalf("%+v %v", second, err)
	}
	e.noOrder(t, e.orderID(t, alice.addr, 1))
	rest, err := e.ek.GetOrder(e.ctx, e.orderID(t, alice.addr, 2))
	if err != nil || rest.Order.RemainingQuantity != 2 || rest.Order.Sequence != 2 {
		t.Fatalf("%+v %v", rest.Order, err)
	}
	e.noOrder(t, e.orderID(t, bob.addr, 1))
	if batch.Results[0].Status != types.StatusResting || batch.Results[1].Status != types.StatusResting || batch.Results[2].Status != types.StatusFilled {
		t.Fatalf("%+v", batch.Results)
	}
	if e.nonce(t, alice.addr) != 2 || e.nonce(t, bob.addr) != 1 {
		t.Fatalf("nonces %d %d", e.nonce(t, alice.addr), e.nonce(t, bob.addr))
	}
}

func TestTwoUsers(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	e.fund(t, bob.addr, quoteAsset, 100)
	sell, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 10)
	buy, _ := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 10)
	batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, sell, buy))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results[1].Trades) != 1 || e.bal(t, alice.addr, quoteAsset).Available != 100 || e.bal(t, bob.addr, baseAsset).Available != 10 {
		t.Fatalf("batch %+v alice %+v bob %+v", batch.Results, e.bal(t, alice.addr, quoteAsset), e.bal(t, bob.addr, baseAsset))
	}
	e.noOrder(t, batch.Results[0].OrderID)
	e.noOrder(t, batch.Results[1].OrderID)
}

func TestBatchNumbers(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(2, 1, mustPlace(t, e, alice, 1))); !errors.Is(err, types.ErrBatchNumber) {
		t.Fatal(err)
	}
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, mustPlace(t, e, alice, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, e.revision(t), mustPlace(t, e, alice, 2))); !errors.Is(err, types.ErrBatchNumber) {
		t.Fatal(err)
	}
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(3, e.revision(t), mustPlace(t, e, alice, 2))); !errors.Is(err, types.ErrBatchNumber) {
		t.Fatal(err)
	}
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(2, e.revision(t), mustPlace(t, e, alice, 2))); err != nil {
		t.Fatal(err)
	}
	latest, err := e.bk.LatestBatch(e.ctx)
	if err != nil || latest.Number != 2 {
		t.Fatalf("%+v %v", latest, err)
	}
	if e.nonce(t, alice.addr) != 2 {
		t.Fatal(e.nonce(t, alice.addr))
	}
}

func TestStaleRevision(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	pb := mustPlace(t, e, alice, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 0, pb)); !errors.Is(err, types.ErrStaleRevision) {
		t.Fatal(err)
	}
	e.noBatch(t)
	e.noOrder(t, e.orderID(t, alice.addr, 1))
	if _, err := e.ek.PlaceOrder(e.ctx, exchangetypes.PlaceOrderCommand{
		Owner: alice.addr, MarketID: mkt, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 10, Quantity: 1, CommandNonce: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rev := e.revision(t)
	pb2, _ := e.place(t, alice, 2, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 11, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, rev-1, pb2)); !errors.Is(err, types.ErrStaleRevision) {
		t.Fatal(err)
	}
	if e.nonce(t, alice.addr) != 1 || e.revision(t) != rev {
		t.Fatalf("nonce %d rev %d", e.nonce(t, alice.addr), e.revision(t))
	}
	e.noBatch(t)
}

func TestUnauthorizedSubmitter(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	msg := e.msg(1, 1, mustPlace(t, e, alice, 1))
	msg.Submitter = alice.addr.String()
	if _, err := e.bk.FinalizeBatch(e.ctx, msg); !errors.Is(err, types.ErrUnauthorized) {
		t.Fatal(err)
	}
	e.noBatch(t)
	if e.nonce(t, alice.addr) != 0 {
		t.Fatal(e.nonce(t, alice.addr))
	}
}

func TestInvalidSignature(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	pb := mustPlace(t, e, alice, 1)
	pb.Signature[len(pb.Signature)-1] ^= 0xff
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, pb)); !errors.Is(err, types.ErrInvalidSignature) {
		t.Fatal(err)
	}
	e.noBatch(t)
	e.noOrder(t, e.orderID(t, alice.addr, 1))
}

func TestOwnerPubkeyMismatch(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, bob.addr, baseAsset, 10)
	cmd := canonical.Command{
		ProtocolVersion: canonical.BatchCommandVersion, ChainID: chainID,
		ExchangeInstanceID: append([]byte(nil), e.instance...), Owner: bob.addr, Nonce: 1,
		Type: canonical.CommandTypePlace,
		Place: &canonical.Place{
			MarketID: mkt, Side: domain.SideSell, Type: domain.OrderTypeLimit,
			TimeInForce: domain.TimeInForceGTC, Quantity: 1, Price: 10,
		},
	}
	pb, _ := e.command(t, alice, cmd)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, pb)); !errors.Is(err, types.ErrPubKeyMismatch) {
		t.Fatal(err)
	}
	e.noBatch(t)
	if e.nonce(t, bob.addr) != 0 {
		t.Fatal(e.nonce(t, bob.addr))
	}
}

func TestWrongChainID(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	pb, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 1)
	pb.ChainId = "other-chain"
	resigned := resign(t, alice, pb, e)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, resigned)); !errors.Is(err, types.ErrChainID) {
		t.Fatal(err)
	}
	e.noBatch(t)
}

func TestWrongInstance(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	pb, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 1)
	pb.ExchangeInstanceId = []byte("other-instance")
	resigned := resign(t, alice, pb, e)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, resigned)); !errors.Is(err, types.ErrInstance) {
		t.Fatal(err)
	}
	e.noBatch(t)
}

func TestWrongNonce(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, mustPlace(t, e, alice, 2))); !errors.Is(err, exchangetypes.ErrWrongNonce) {
		t.Fatal(err)
	}
	e.noBatch(t)
	if e.nonce(t, alice.addr) != 0 || e.revision(t) != 1 {
		t.Fatalf("nonce %d rev %d", e.nonce(t, alice.addr), e.revision(t))
	}
}

func TestDirectCommandStaleNonce(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	if _, err := e.ek.PlaceOrder(e.ctx, exchangetypes.PlaceOrderCommand{
		Owner: alice.addr, MarketID: mkt, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 10, Quantity: 4, CommandNonce: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rev := e.revision(t)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, rev, mustPlace(t, e, alice, 1))); !errors.Is(err, exchangetypes.ErrWrongNonce) {
		t.Fatal(err)
	}
	got, err := e.ek.GetOrder(e.ctx, e.orderID(t, alice.addr, 1))
	if err != nil || got.Order.RemainingQuantity != 4 || e.nonce(t, alice.addr) != 1 || e.revision(t) != rev {
		t.Fatalf("%+v nonce %d rev %d", got.Order, e.nonce(t, alice.addr), e.revision(t))
	}
	e.noBatch(t)
}

func TestDuplicateNonceRollsBack(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	first := mustPlace(t, e, alice, 1)
	second := mustPlace(t, e, alice, 1)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, first, second)); !errors.Is(err, exchangetypes.ErrWrongNonce) {
		t.Fatal(err)
	}
	e.noOrder(t, e.orderID(t, alice.addr, 1))
	if e.nonce(t, alice.addr) != 0 || e.bal(t, alice.addr, baseAsset).Available != 10 || e.bal(t, alice.addr, baseAsset).Locked != 0 {
		t.Fatalf("nonce %d bal %+v", e.nonce(t, alice.addr), e.bal(t, alice.addr, baseAsset))
	}
	e.noBatch(t)
}

func TestCancelInBatch(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	sell, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	id := e.orderID(t, alice.addr, 1)
	cancel, _ := e.cancel(t, alice, 2, id)
	batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, sell, cancel))
	if err != nil {
		t.Fatal(err)
	}
	if batch.Results[1].Status != types.StatusCancelled || batch.PostRevision != 3 {
		t.Fatalf("%+v", batch)
	}
	e.noOrder(t, id)
	if e.nonce(t, alice.addr) != 2 || e.bal(t, alice.addr, baseAsset) != (exchangetypes.Balance{Available: 10}) {
		t.Fatalf("nonce %d bal %+v", e.nonce(t, alice.addr), e.bal(t, alice.addr, baseAsset))
	}
	seq, err := e.ek.GetTradeSequence(e.ctx, mkt)
	if err != nil || seq != 0 {
		t.Fatal(seq, err)
	}
}

func TestSelfTradeUnchanged(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	e.fund(t, alice.addr, quoteAsset, 100)
	sell, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 5)
	buy, _ := e.place(t, alice, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 6, 5)
	batch, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, sell, buy))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results[1].Trades) != 0 || batch.Results[1].Status != types.StatusUnfilled || batch.Results[0].Status != types.StatusResting {
		t.Fatalf("%+v", batch.Results)
	}
	got, err := e.ek.GetOrder(e.ctx, e.orderID(t, alice.addr, 1))
	if err != nil || got.Order.RemainingQuantity != 5 {
		t.Fatalf("%+v %v", got.Order, err)
	}
	e.noOrder(t, e.orderID(t, alice.addr, 2))
	if e.bal(t, alice.addr, quoteAsset).Available != 100 || e.bal(t, alice.addr, quoteAsset).Locked != 0 || e.nonce(t, alice.addr) != 2 {
		t.Fatalf("quote %+v nonce %d", e.bal(t, alice.addr, quoteAsset), e.nonce(t, alice.addr))
	}
}

func TestFOKRejectsBatch(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	e.fund(t, bob.addr, quoteAsset, 100)
	sell, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 4, 4)
	fok, _ := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceFOK, 4, 5)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, sell, fok)); !errors.Is(err, exchangetypes.ErrFOKRejected) {
		t.Fatal(err)
	}
	e.noOrder(t, e.orderID(t, alice.addr, 1))
	if e.nonce(t, alice.addr) != 0 || e.nonce(t, bob.addr) != 0 || e.revision(t) != 1 || e.bal(t, bob.addr, quoteAsset).Available != 100 {
		t.Fatalf("nonces %d %d rev %d", e.nonce(t, alice.addr), e.nonce(t, bob.addr), e.revision(t))
	}
	e.noBatch(t)
}

func TestFailureRollsBack(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	e.fund(t, bob.addr, quoteAsset, 28)
	if _, err := e.ek.PlaceOrder(e.ctx, exchangetypes.PlaceOrderCommand{
		Owner: alice.addr, MarketID: mkt, Side: domain.SideSell, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC, Price: 10, Quantity: 5, CommandNonce: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rev := e.revision(t)
	orderSeq, err := e.ek.GetOrderSequence(e.ctx, mkt)
	if err != nil || orderSeq != 1 {
		t.Fatal(orderSeq, err)
	}
	match, _ := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 2)
	rest, _ := e.place(t, bob, 2, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 8, 1)
	over, _ := e.place(t, bob, 3, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 10)
	e.ctx = e.ctx.WithEventManager(sdk.NewEventManager())
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, rev, match, rest, over)); !errors.Is(err, exchangetypes.ErrInsufficientBalance) {
		t.Fatal(err)
	}
	got, err := e.ek.GetOrder(e.ctx, e.orderID(t, alice.addr, 1))
	if err != nil || got.Order.RemainingQuantity != 5 {
		t.Fatalf("%+v %v", got.Order, err)
	}
	e.noOrder(t, e.orderID(t, bob.addr, 2))
	if _, err := e.ek.GetTrade(e.ctx, mkt, 1); !errors.Is(err, exchangetypes.ErrNotFound) {
		t.Fatal(err)
	}
	tradeSeq, err := e.ek.GetTradeSequence(e.ctx, mkt)
	if err != nil || tradeSeq != 0 {
		t.Fatal(tradeSeq, err)
	}
	orderSeq, err = e.ek.GetOrderSequence(e.ctx, mkt)
	if err != nil || orderSeq != 1 {
		t.Fatal(orderSeq, err)
	}
	if e.nonce(t, bob.addr) != 0 || e.nonce(t, alice.addr) != 1 || e.revision(t) != rev {
		t.Fatalf("bob %d alice %d rev %d", e.nonce(t, bob.addr), e.nonce(t, alice.addr), e.revision(t))
	}
	if e.bal(t, bob.addr, quoteAsset) != (exchangetypes.Balance{Available: 28}) {
		t.Fatal(e.bal(t, bob.addr, quoteAsset))
	}
	e.noBatch(t)
	if hasEvent(e.ctx, types.EventTypeBatchFinalized) {
		t.Fatal("batch event committed")
	}
}

func TestBatchIDCommandOrder(t *testing.T) {
	e := setup(t)
	alice, bob := newTrader(), newTrader()
	sell, sellCmd := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 1)
	buy, buyCmd := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 9, 1)
	_ = sell
	_ = buy
	forward, err := canonical.HashBatchID(1, 1, []canonical.Command{sellCmd, buyCmd})
	if err != nil {
		t.Fatal(err)
	}
	same, err := canonical.HashBatchID(1, 1, []canonical.Command{sellCmd, buyCmd})
	if err != nil || forward != same {
		t.Fatal(err)
	}
	backward, err := canonical.HashBatchID(1, 1, []canonical.Command{buyCmd, sellCmd})
	if err != nil || forward == backward {
		t.Fatal("command order did not change the batch id")
	}
}

func TestEmptyBatch(t *testing.T) {
	e := setup(t)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1)); !errors.Is(err, types.ErrEmpty) {
		t.Fatal(err)
	}
}

func mustPlace(t *testing.T, e env, tr trader, nonce uint64) *v1.SignedCommand {
	t.Helper()
	pb, _ := e.place(t, tr, nonce, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 1)
	return pb
}

func resign(t *testing.T, tr trader, pb *v1.SignedCommand, e env) *v1.SignedCommand {
	t.Helper()
	cmd, err := commandFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	bz, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := tr.priv.Sign(bz)
	if err != nil {
		t.Fatal(err)
	}
	pb.PubKey = tr.priv.PubKey().Bytes()
	pb.Signature = sig
	return pb
}

func TestResultBytesMatchBatchStatus(t *testing.T) {
	if canonical.ResultResting != types.StatusResting ||
		canonical.ResultFilled != types.StatusFilled ||
		canonical.ResultCancelled != types.StatusCancelled ||
		canonical.ResultUnfilled != types.StatusUnfilled ||
		byte(canonical.CommandTypePlace) != types.CommandPlace ||
		byte(canonical.CommandTypeCancel) != types.CommandCancel {
		t.Fatal("result bytes drifted")
	}
}

func TestCommitmentChain(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	bob := newTrader()
	e.fund(t, alice.addr, baseAsset, 20)
	e.fund(t, bob.addr, quoteAsset, 1000)
	if !bytes.Equal(canonical.GenesisBatchCommitment[:], make([]byte, 32)) {
		t.Fatal("genesis commitment")
	}
	sell, _ := e.place(t, alice, 1, domain.SideSell, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	wrong := e.msg(1, 1, sell)
	wrong.PreviousBatchCommitment = bytes.Repeat([]byte{0xab}, 32)
	if _, err := e.bk.FinalizeBatch(e.ctx, wrong); !errors.Is(err, types.ErrPreviousCommitment) {
		t.Fatal(err)
	}
	e.noBatch(t)
	if e.nonce(t, alice.addr) != 0 {
		t.Fatal(e.nonce(t, alice.addr))
	}

	first, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, sell))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Previous.IsZero() || first.Commitment.IsZero() || first.ResultsHash.IsZero() {
		t.Fatalf("%x %x %x", first.Previous, first.Commitment, first.ResultsHash)
	}
	if e.head(t) != first.Commitment {
		t.Fatal("head is not the first commitment")
	}
	rh, cm := recompute(t, e, first)
	if rh != first.ResultsHash || cm != first.Commitment {
		t.Fatal("stored commitment does not match the canonical digest")
	}
	againRH, againCM := recompute(t, e, first)
	if againRH != rh || againCM != cm {
		t.Fatal("replaying the batch changed the digests")
	}

	stale := e.msg(2, e.revision(t), mustPlace(t, e, bob, 1))
	stale.PreviousBatchCommitment = make([]byte, 32)
	if _, err := e.bk.FinalizeBatch(e.ctx, stale); !errors.Is(err, types.ErrPreviousCommitment) {
		t.Fatal(err)
	}
	if e.head(t) != first.Commitment || e.nonce(t, bob.addr) != 0 || e.revision(t) != first.PostRevision {
		t.Fatal("stale previous commitment changed state")
	}
	if _, err := e.bk.GetBatch(e.ctx, 2); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}

	buy, _ := e.place(t, bob, 1, domain.SideBuy, domain.OrderTypeLimit, domain.TimeInForceGTC, 10, 4)
	second, err := e.bk.FinalizeBatch(e.ctx, e.msg(2, e.revision(t), buy))
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous != first.Commitment || second.Commitment == first.Commitment || e.head(t) != second.Commitment {
		t.Fatalf("chain prev %x c1 %x c2 %x head %x", second.Previous, first.Commitment, second.Commitment, e.head(t))
	}
	if _, cm2 := recompute(t, e, second); cm2 != second.Commitment {
		t.Fatal("second commitment drifted")
	}

	third := e.msg(3, e.revision(t), mustPlace(t, e, alice, 2))
	third.PreviousBatchCommitment = append([]byte(nil), first.Commitment[:]...)
	if _, err := e.bk.FinalizeBatch(e.ctx, third); !errors.Is(err, types.ErrPreviousCommitment) {
		t.Fatal(err)
	}
	if e.head(t) != second.Commitment {
		t.Fatal("stale head replaced the current commitment")
	}
	if _, err := e.bk.GetBatch(e.ctx, 3); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	qs := NewQueryServer(e.bk)
	q1, err := qs.BatchCommitment(e.ctx, &v1.QueryBatchCommitmentRequest{BatchNumber: 1})
	if err != nil || !bytes.Equal(q1.BatchCommitment, first.Commitment[:]) || !bytes.Equal(q1.ResultsHash, first.ResultsHash[:]) || !bytes.Equal(q1.BatchId, first.ID[:]) {
		t.Fatalf("%+v %v", q1, err)
	}
	latest, err := qs.LatestBatch(e.ctx, &v1.QueryLatestBatchRequest{})
	if err != nil || !bytes.Equal(latest.Batch.BatchCommitment, second.Commitment[:]) || !bytes.Equal(latest.Batch.PreviousBatchCommitment, first.Commitment[:]) {
		t.Fatal(err)
	}
}

func TestFailedBatchLeavesHead(t *testing.T) {
	e := setup(t)
	alice := newTrader()
	e.fund(t, alice.addr, baseAsset, 10)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(1, 1, mustPlace(t, e, alice, 1))); err != nil {
		t.Fatal(err)
	}
	first, err := e.bk.GetBatch(e.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	rev := e.revision(t)
	if _, err := e.bk.FinalizeBatch(e.ctx, e.msg(2, rev, mustPlace(t, e, alice, 2), mustPlace(t, e, alice, 2))); !errors.Is(err, exchangetypes.ErrWrongNonce) {
		t.Fatal(err)
	}
	if e.head(t) != first.Commitment || e.revision(t) != rev || e.nonce(t, alice.addr) != 1 {
		t.Fatalf("head %x nonce %d rev %d", e.head(t), e.nonce(t, alice.addr), e.revision(t))
	}
	if _, err := e.bk.GetBatch(e.ctx, 2); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	latest, err := e.bk.LatestBatch(e.ctx)
	if err != nil || latest.Number != 1 || latest.Commitment != first.Commitment || latest.ResultsHash != first.ResultsHash {
		t.Fatalf("%+v %v", latest, err)
	}
}

func recompute(t *testing.T, e env, batch types.Batch) (types.ResultsHash, types.BatchCommitment) {
	t.Helper()
	results := make([]canonical.Result, len(batch.Results))
	for i, result := range batch.Results {
		trades := make([]canonical.ResultTrade, len(result.Trades))
		for j, trade := range result.Trades {
			trades[j] = canonical.ResultTrade{MarketID: trade.MarketID, Sequence: trade.Sequence}
		}
		results[i] = canonical.Result{
			Index: result.Index, Type: canonical.CommandType(result.Type), Owner: result.Owner,
			OrderID: result.OrderID, Status: result.Status, Remaining: result.Remaining, Trades: trades,
		}
	}
	rh, err := canonical.HashResults(results)
	if err != nil {
		t.Fatal(err)
	}
	cm, err := canonical.HashBatchCommitment(canonical.BatchCommitmentInput{
		Version: canonical.BatchCommitmentVersion, ChainID: chainID, ExchangeInstanceID: e.instance,
		BatchNumber: batch.Number, BatchID: [32]byte(batch.ID), PreviousBatchCommitment: [32]byte(batch.Previous),
		ExecutionHeight: batch.Height, PreExchangeRevision: batch.PreRevision,
		PostExchangeRevision: batch.PostRevision, ResultsHash: rh,
	})
	if err != nil {
		t.Fatal(err)
	}
	return types.ResultsHash(rh), types.BatchCommitment(cm)
}

func TestBatchCodecRoundTrip(t *testing.T) {
	owner := bytes.Repeat([]byte{0x02}, 20)
	var id domain.OrderID
	id[0] = 1
	var batchID types.BatchID
	batchID[0] = 2
	var head, commitment types.BatchCommitment
	head[0] = 3
	commitment[0] = 5
	var rh types.ResultsHash
	rh[0] = 4
	batch := types.Batch{
		Number: 2, ID: batchID, Height: 9, PreRevision: 3, PostRevision: 4,
		Previous: head, Commitment: commitment, ResultsHash: rh,
		Results: []types.CommandResult{{
			Index: 0, Type: types.CommandPlace, Owner: owner, OrderID: id,
			Status: types.StatusFilled, Trades: []types.TradeRef{{MarketID: 1, Sequence: 1}},
		}},
	}
	encoded, err := types.EncodeBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := types.DecodeBatch(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != batch.ID || got.Previous != batch.Previous || got.Commitment != batch.Commitment || got.ResultsHash != batch.ResultsHash || got.Height != 9 {
		t.Fatalf("%+v", got)
	}
	if len(got.Results) != 1 || got.Results[0].OrderID != id || got.Results[0].Trades[0].Sequence != 1 {
		t.Fatalf("%+v", got.Results)
	}
	params := types.Params{Submitter: owner, Latest: 2, Head: commitment}
	raw, err := types.EncodeParams(params)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := types.DecodeParams(raw)
	if err != nil || decoded.Latest != 2 || decoded.Head != commitment || !bytes.Equal(decoded.Submitter, owner) {
		t.Fatalf("%+v %v", decoded, err)
	}
	if _, err := types.EncodeParams(types.Params{Submitter: owner, Head: head}); err == nil {
		t.Fatal("genesis head must be zero")
	}
}

func hasEvent(ctx sdk.Context, typ string) bool {
	for _, ev := range ctx.EventManager().Events() {
		if ev.Type == typ {
			return true
		}
	}
	return false
}
