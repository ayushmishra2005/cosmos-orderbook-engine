# cosmos-orderbook-engine

> High-performance Cosmos SDK exchange engine with a deterministic CLOB, order matcher, trade execution coordinator, settlement modules and batched Layer-2 execution environment.

The project goal is a serious Cosmos SDK central limit order book: integer ticks and lots, price-time priority, and a match plan that validators can replay. This repository is that engine. It is not a production rollup, and it does not implement fraud proofs, validity proofs, or a decentralized sequencer.

## Status

**Implemented — milestone 1, deterministic matching foundation**

- Domain types, checked `uint64` arithmetic, SHA-256 order IDs, sequence and nonce primitives
- Pure price-time matcher and an in-memory cursor with the same byte order as the book keys

**Implemented — milestone 2, `x/exchange`**

- Persistent markets, orders, available/locked balances, and fee-collector balances
- Place, cancel, and bounded expiration
- Reservation, maker-price settlement, buy price improvement, and cumulative maker/taker fees
- Trades, order and trade sequences, command nonces, and exchange revision
- Atomic place/cancel via a Cosmos SDK cache context

**Implemented — milestone 3, runnable chain**

- `cosmos-orderbookd` wires auth, bank, staking, genutil, consensus, and `x/exchange`
- Deposit and withdrawal move bank coins through one exchange module account
- `Msg` and query services, genesis, and CLI commands for orders, balances, the book, and trades

**Implemented — milestone 4, `x/batch`**

- One authorized submitter finalizes an ordered batch of signed place and cancel commands
- Validators execute those commands through `x/exchange` in submitted order
- The batch is atomic: a failed command rolls back earlier commands, nonces, and the batch record
- `BatchID` identifies the ordered batch. It is not an exchange state root

**Planned**

- Sequencer admission and batch building

`pkg/matching` still does not import the SDK.

## Architecture

```text
pkg/domain → pkg/arithmetic → pkg/matching
pkg/canonical → pkg/domain
```

The matcher returns a `MatchPlan`. It does not write state. Book order comes from the key layout: asks sort by ascending price, bids sort by `MaxUint64 - price`, and sequence breaks ties in favor of the older order. Details are in [docs/architecture.md](docs/architecture.md), [docs/matching-engine.md](docs/matching-engine.md), and [docs/state-layout.md](docs/state-layout.md).

## Repository

```text
pkg/domain          orders, sides, sequence, nonce
pkg/arithmetic      checked integer math
pkg/canonical       order IDs, state keys, batch command bytes
pkg/matching        pure matcher
pkg/matching/memsource
x/exchange          keeper, module, messages, queries
x/batch             ordered batch execution
app                 chain wiring
cmd/cosmos-orderbookd
benchmarks          matcher benchmarks
docs                architecture, matching, state layout
```

The sequencer is not in the tree yet.

## Matching example

Ticks, not decimals. A buy of 100 lots at tick 1000 against asks of 30 @ 980, 40 @ 990, and 50 @ 1000 fills 30 @ 980, 40 @ 990, and 30 @ 1000. The incoming remainder is 0. The last maker keeps 20 lots and its original sequence.

A UI can render tick 1000 as `10.00`. That rendering is not part of consensus. See [docs/architecture.md](docs/architecture.md).

## Local node

```bash
make build
./scripts/localnet.sh
```

The script initializes one validator, funds `alice` with `base` and `bob` with `quote`, and starts the node. Home defaults to `.localnet`. After the node is up:

```bash
build/cosmos-orderbookd tx exchange deposit 1000base --from alice --chain-id orderbook-1 --keyring-backend test --home .localnet --fees 1stake -y
build/cosmos-orderbookd q exchange markets --home .localnet
```

## Tests

```bash
go test ./...
go test -race ./...
go vet ./...
```

`make check` runs all three. Fuzz targets are included; `go test` executes their seed corpus. A longer run is `go test -fuzz=FuzzMatchReplay -fuzztime=10s ./pkg/matching`.

## Benchmarks

```bash
go test -bench=. -benchmem -count=1 -run=^$ ./benchmarks/
```

Or `make bench`. The harness prints `ns/op`, `allocs/op`, and `bytes/op`. This repository does not publish an orders-per-second number. Treat benchmark output as a measurement of the machine that ran it.

## License

Apache-2.0. See [LICENSE](LICENSE).
