package app

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

type serverOpts map[string]any

func (o serverOpts) Get(key string) any { return o[key] }

func TestConfiguredMinGasPricesRejectCheckTx(t *testing.T) {
	home := t.TempDir()
	opts := serverOpts{
		server.FlagPruning:       "nothing",
		server.FlagMinGasPrices:  "1stake",
		server.FlagMempoolMaxTxs: 1000,
		flags.FlagHome:           home,
		flags.FlagChainID:        testChainID,
	}
	alice := newTrader()
	application, err := New(log.NewNopLogger(), dbm.NewMemDB(), testChainID, server.DefaultBaseappOptions(opts)...)
	require.NoError(t, err)
	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	genesis := application.DefaultGenesis()
	genesis, err = simtestutil.GenesisStateWithValSet(
		application.Codec(),
		genesis,
		valSet,
		[]authtypes.GenesisAccount{authtypes.NewBaseAccount(alice.addr, alice.priv.PubKey(), 0, 0)},
		banktypes.Balance{Address: alice.addr.String(), Coins: coins("stake", 1_000_000_000, "base", 100)},
	)
	require.NoError(t, err)
	state, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = application.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes:   state,
		Time:            time.Unix(1_700_000_000, 0).UTC(),
		InitialHeight:   1,
	})
	require.NoError(t, err)
	_, err = application.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:             1,
		Time:               time.Unix(1_700_000_000, 0).UTC(),
		NextValidatorsHash: valSet.Hash(),
	})
	require.NoError(t, err)
	_, err = application.Commit()
	require.NoError(t, err)

	msg := []sdk.Msg{&exchangev1.MsgDeposit{
		Owner: alice.addr.String(), Amount: sdk.NewInt64Coin("base", 10),
	}}
	low := signedTx(t, application, alice, msg, sdk.NewCoins(sdk.NewInt64Coin("stake", 0)), 200_000, 0, 0)
	lowRes, err := application.CheckTx(&abci.RequestCheckTx{Tx: low, Type: abci.CheckTxType_New})
	require.NoError(t, err)
	require.NotZero(t, lowRes.Code)

	okBytes := signedTx(t, application, alice, msg, sdk.NewCoins(sdk.NewInt64Coin("stake", 200_000)), 200_000, 0, 0)
	okRes, err := application.CheckTx(&abci.RequestCheckTx{Tx: okBytes, Type: abci.CheckTxType_New})
	require.NoError(t, err)
	require.Zero(t, okRes.Code)

	_, err = application.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: nextExecutionHeight(application),
		Time:   time.Unix(1_700_000_100, 0).UTC(),
		Txs:    [][]byte{okBytes},
	})
	require.NoError(t, err)
	_, err = application.Commit()
	require.NoError(t, err)
	require.Equal(t, uint64(10), mustBal(t, application, alice.addr, 1).Available)
}

func signedTx(t *testing.T, application *App, tr trader, msgs []sdk.Msg, fee sdk.Coins, gas, accNum, seq uint64) []byte {
	t.Helper()
	tx, err := simtestutil.GenSignedMockTx(
		rand.New(rand.NewSource(1)), application.TxConfig(), msgs, fee, gas, testChainID,
		[]uint64{accNum}, []uint64{seq}, tr.priv,
	)
	require.NoError(t, err)
	bz, err := application.TxConfig().TxEncoder()(tx)
	require.NoError(t, err)
	return bz
}
