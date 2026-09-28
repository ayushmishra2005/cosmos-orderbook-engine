# Architecture

`cosmos-orderbook-engine` is a Cosmos SDK exchange. Matching is a pure function. `x/exchange` owns the store, reservations, settlement, and fees. `x/batch` submits an ordered list of signed commands and runs them through that keeper. The chain binary wires auth, bank, staking, genutil, consensus, exchange, and batch. `orderbook-sequencer` is an off-chain process. It does not decide fills, balances, or fees.

Toolchain: Go 1.26.x and Cosmos SDK v0.54.4 (CometBFT v0.39.4 via that SDK). The matcher does not import the SDK.

## Packages

```text
pkg/domain
    ↓
pkg/arithmetic
    ↓
pkg/matching
    ↓
x/exchange
    ↑
x/batch
```

`pkg/canonical` depends only on `pkg/domain`. It encodes order IDs, state keys, signed batch commands, command results, and batch commitments. `x/exchange` may import the packages above. `x/batch` may call `x/exchange`. `x/exchange` must not import `x/batch`. `pkg/*` must not import `x/exchange`, `x/batch`, the app, or a Cosmos keeper.

`Match` returns a plan. It does not write balances, delete orders, or assign sequences. The keeper opens one side of one market as an `OrderSource`, calls `Match`, closes the cursor, and only then builds an execution plan. One place or cancel runs inside a single `CacheContext`. The cache is written only after the plan validates. Any error discards it.

There is no `x/orderbook` or `x/settlement` module.

## Numeric model

Prices are tick counts. Quantities are lot counts. Both are `uint64`. Display decimals are not protocol values.

```text
baseAmount  = quantityLots * baseLotSize
quoteAmount = quantityLots * priceTicks * quoteAtomsPerTickPerLot
```

No `float32` or `float64` on the match or settlement path. Addition, subtraction, and multiplication are checked. Overflow and underflow return errors and do not wrap. A zero lot size or a zero quote-atoms-per-tick is an error.

Fees use parts per million:

```text
C(gross, ppm) = ceil(gross * ppm / 1_000_000)
```

A fee that does not fit in `uint64` is an error. Rates above `1_000_000` are rejected so a fee cannot exceed the gross it is charged on. Buyer fees are base atoms. Seller fees are quote atoms. The maker or taker rate follows the order's role on that fill.

Split fills must not change the total fee. The order stores separate taker and maker gross accumulators. The fee on a new gross is `C(previous + new) - C(previous)` for that role. An order that first takes and later rests does not mix those accumulators.

## Order IDs, nonces, sequences

`OrderID` is 32 bytes:

```text
SHA-256(
    u64be len(domain) | "cosmos-orderbook-engine/order-id/v1"
    u64be len(chainID) | chainID
    u64be len(instance) | instance
    u64be len(owner) | owner
    u64be marketID
    u64be commandNonce
)
```

Variable-length fields are length-prefixed. The encoding is not JSON and not protobuf. `owner` is raw address bytes, not bech32. The hash does not check the nonce sequence. The keeper does: the command nonce must equal `ExpectedCommandNonce(lastAccepted)`. The first accepted nonce is 1. Nonce 0 means none have been accepted.

Each market has one order sequence. `NextSequence(0)` is 1. It is allocated only when an order rests. A lower sequence is older and wins at the same price. A partial fill does not take a new sequence. Trade sequence is a separate per-market counter and also starts at 1. Neither counter wraps.

Key bytes are specified in [state-layout.md](state-layout.md).

## Execution

A buy locks `quantity * limitTick * quoteAtomsPerTickPerLot` quote atoms. A sell locks `quantity * baseLotSize` base atoms. A market buy locks quote at its maximum tick. A market sell locks base. Market orders never rest.

The fill price is the maker tick. The buyer receives base minus the buyer fee. The seller receives quote minus the seller fee. Fees accrue to the fee-collector balance of that asset. When an incoming buy fills below its limit, the unused locked quote is returned to available quote. A resting buy fills at its own tick, so that fill has no price-improvement refund.

Place and cancel either commit every exchange write or leave the store unchanged. Matching reads a cursor and does not write. The cache is written only after the execution plan checks out.

## Batch execution

`MsgFinalizeBatch` carries a batch number, the exchange revision the commands were built against, the previous batch commitment, and ordered `PlaceOrder` and `CancelOrder` commands. Deposits and withdrawals stay ordinary transactions. Validators execute the commands themselves, in the submitted order, through `x/exchange`. Later commands see the balances, orders, nonces, and revision written by earlier commands in the same batch.

The batch is one cache around those calls. The exchange keeper still opens its own cache for each command. If any check or command fails, neither cache is kept: orders, trades, nonces, the revision, the batch record, the results hash, and the commitment head all stay as they were. There is no partial batch.

Each command is signed by its owner with secp256k1 over canonical bytes (`cosmos-orderbook/batch-command/v1`), not JSON or protobuf. The public key must derive the owner. The chain ID, exchange instance, and protocol version must match. The command uses the exchange account nonce, the same nonce as a direct place or cancel.

One genesis address may submit batches. That authorization is temporary and centralized. The off-chain sequencer uses that submitter. There is no submitter rotation or proof system.

The first finalized batch number is 1. The next number is the previous number plus one. A duplicate, skipped, or older number is rejected. The current exchange revision must equal `expectedExchangeRevision` before the first command runs. `BeginBlock` expiration can advance the revision before transactions in that block.

`BatchID` is SHA-256 over `cosmos-orderbook/batch-id/v1`, the batch number, the expected revision, and the ordered signed commands. It identifies that batch. It is not an exchange state root.

`ResultsHash` is SHA-256 over `cosmos-orderbook/batch-results/v1` and the ordered command results. Each result is the command index, type, owner, order ID, final status, remaining quantity, and the ordered trade sequences. Results stay in command order.

`BatchCommitment` is SHA-256 over `cosmos-orderbook/batch-commitment/v1`, version 1, chain ID, exchange instance, batch number, `BatchID`, the previous commitment, execution height, pre and post exchange revisions, and `ResultsHash`. The height is the consensus block height. Batch 1's previous commitment is 32 zero bytes. A later batch must name the current head. The head changes only after every command succeeds.

`BatchCommitment` binds the accepted batch to its previous commitment and its execution result. It does not prove the full exchange state. Validators still re-execute the batch. It is not a validity proof, a fraud proof, or a data-availability proof.

## Sequencer

`sequencer` accepts the same canonical signed commands as `x/batch`. HTTP JSON is only transport. One lock assigns the next position and appends the command to a local journal before the caller is told it was accepted. That acceptance is provisional.

The batch loop reads the current batch head and exchange revision, freezes the oldest pending commands, and submits `MsgFinalizeBatch` through a normal Cosmos transaction. The submitter key stays in the SDK keyring. A rejected batch returns its commands to pending, except a nonce the chain has already passed, which is dropped. Finalized commands are not queued again after a restart.

The journal is not chain state. It is not an exchange state root.

Admission metrics, batch timings, and the retry delay are local to the sequencer process. A failed batch is not broadcast again until that delay elapses. The delay doubles up to a cap and resets after a batch is included. It does not change command order. A rejected command stays pending unless its nonce is already behind the chain. An underfunded order is not dropped; a later deposit can make it valid.

`GET /health` reports pending and in-flight counts, the latest observed batch number, and whether the last chain call succeeded. `GET /metrics` serves Prometheus text. Neither response is an input to matching, fees, ordering, batch hashes, or state writes. A metrics recorder that panics is ignored. Wall-clock samples used for histograms are not written to the store.

## Checks

`CheckInvariants` and `CheckCustody` read the store and return an error. They do not write, and they do not repair a bad key. `StateDigestForTest` and `SnapshotDigest` hash KV pairs in key order so tests can compare validators. The digest is not a batch commitment, a state root, or a proof.

A fee-collector balance is one of the internal balances. Custody is the sum of available and locked atoms, including that balance. The exchange module account's bank balance must cover the sum. It may be larger. Trades do not move bank coins.

`scripts/localnet4.sh` runs four CometBFT validators on one genesis. Each validator executes the same transactions. Wall-clock samples used by metrics are not inputs to that execution.

## Application

`cosmos-orderbookd` can run as one validator or as the four-validator local network. Deposits move bank coins from the user to the exchange module account and credit available balance. Withdrawals do the reverse and cannot spend locked balance. Both run in one cache, so a bank failure does not leave the internal ledger changed. Trades move atoms only inside the exchange ledger. The module account's bank balance for each registered denom must cover the sum of available and locked balances.

Genesis may register assets, markets, sequences, and the exchange revision. An internal balance is accepted only when that module custody already holds the coins. The usual local chain funds bank accounts in genesis and deposits after start. The order ID instance is `orderbook-v1`. Clients do not choose a new order's ID.
