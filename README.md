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

**Planned**

- Full Cosmos app and CometBFT wiring
- `x/batch` execution
- Sequencer admission, signatures, ordering, and batch building
- Protobuf queries and CLI
- Bank deposit and withdrawal messages

`pkg/matching` still does not import the SDK. There is no gRPC, REST, or CLI in this repository yet.

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
pkg/canonical       order IDs and state keys
pkg/matching        pure matcher
pkg/matching/memsource
x/exchange          keeper, book, ledger, settlement
benchmarks          matcher benchmarks
docs                architecture, matching, state layout
```

`x/batch`, the sequencer, and the Cosmos app are not in the tree yet.

## Matching example

Ticks, not decimals. A buy of 100 lots at tick 1000 against asks of 30 @ 980, 40 @ 990, and 50 @ 1000 fills 30 @ 980, 40 @ 990, and 30 @ 1000. The incoming remainder is 0. The last maker keeps 20 lots and its original sequence.

A UI can render tick 1000 as `10.00`. That rendering is not part of consensus. See [docs/architecture.md](docs/architecture.md).

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
