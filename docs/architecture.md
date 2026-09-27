# Architecture

`cosmos-orderbook-engine` is a Cosmos SDK exchange. Matching is a pure function. `x/exchange` owns the store, reservations, settlement, and fees. The chain binary wires auth, bank, staking, genutil, consensus, and exchange. `x/batch` and the sequencer are not implemented.

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
```

`pkg/canonical` depends only on `pkg/domain`. It encodes order IDs and state keys. `x/exchange` may import the packages above. `pkg/*` must not import `x/exchange`, the app, or a Cosmos keeper.

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

## Application

`cosmos-orderbookd` is a single-validator CometBFT chain. Deposits move bank coins from the user to the exchange module account and credit available balance. Withdrawals do the reverse and cannot spend locked balance. Both run in one cache, so a bank failure does not leave the internal ledger changed. Trades move atoms only inside the exchange ledger. The module account's bank balance for each registered denom must cover the sum of available and locked balances.

Genesis may register assets, markets, sequences, and the exchange revision. An internal balance is accepted only when that module custody already holds the coins. The usual local chain funds bank accounts in genesis and deposits after start. The order ID instance is `orderbook-v1`. Clients do not choose a new order's ID.
