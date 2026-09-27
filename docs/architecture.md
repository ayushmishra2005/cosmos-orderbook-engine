# Architecture

`cosmos-orderbook-engine` is a Cosmos SDK exchange. Matching is a pure function. `x/exchange` owns the store, reservations, settlement, and fees. `x/batch` submits an ordered list of signed commands and runs them through that keeper. The chain binary wires auth, bank, staking, genutil, consensus, exchange, and batch. The sequencer is not implemented.

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

`pkg/canonical` depends only on `pkg/domain`. It encodes order IDs, state keys, and signed batch commands. `x/exchange` may import the packages above. `x/batch` may call `x/exchange`. `x/exchange` must not import `x/batch`. `pkg/*` must not import `x/exchange`, `x/batch`, the app, or a Cosmos keeper.

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

`MsgFinalizeBatch` carries a batch number, the exchange revision the commands were built against, and ordered `PlaceOrder` and `CancelOrder` commands. Deposits and withdrawals stay ordinary transactions. Validators execute the commands themselves, in the submitted order, through `x/exchange`. Later commands see the balances, orders, nonces, and revision written by earlier commands in the same batch.

The batch is one cache around those calls. The exchange keeper still opens its own cache for each command. If any check or command fails, neither cache is kept: orders, trades, nonces, the revision, and the batch record all stay as they were. There is no partial batch.

Each command is signed by its owner with secp256k1 over canonical bytes (`cosmos-orderbook/batch-command/v1`), not JSON or protobuf. The public key must derive the owner. The chain ID, exchange instance, and protocol version must match. The command uses the exchange account nonce, the same nonce as a direct place or cancel.

For this milestone one genesis address may submit batches. That authorization is temporary and centralized. There is no sequencer, submitter rotation, or proof system.

The first finalized batch number is 1. The next number is the previous number plus one. A duplicate, skipped, or older number is rejected. The current exchange revision must equal `expectedExchangeRevision` before the first command runs. `BeginBlock` expiration can advance the revision before transactions in that block.

`BatchID` is SHA-256 over `cosmos-orderbook/batch-id/v1`, the batch number, the expected revision, and the ordered signed commands. It identifies that batch. It is not an exchange state root.

## Application

`cosmos-orderbookd` is a single-validator CometBFT chain. Deposits move bank coins from the user to the exchange module account and credit available balance. Withdrawals do the reverse and cannot spend locked balance. Both run in one cache, so a bank failure does not leave the internal ledger changed. Trades move atoms only inside the exchange ledger. The module account's bank balance for each registered denom must cover the sum of available and locked balances.

Genesis may register assets, markets, sequences, and the exchange revision. An internal balance is accepted only when that module custody already holds the coins. The usual local chain funds bank accounts in genesis and deposits after start. The order ID instance is `orderbook-v1`. Clients do not choose a new order's ID.
