# cosmos-orderbook-engine

> High-performance Cosmos SDK exchange engine with a deterministic CLOB, order matcher, trade execution coordinator, settlement modules and batched Layer-2 execution environment.

The project goal is a serious Cosmos SDK central limit order book: integer ticks and lots, price-time priority, and a match plan that validators can replay. This repository is that engine. It is not a production rollup, and it does not implement fraud proofs, validity proofs, or a decentralized sequencer.

## Status

**Implemented — milestone 1, deterministic matching foundation**

- Domain types for markets, ticks, lots, sides, time in force, and orders
- Checked `uint64` arithmetic for addition, subtraction, multiplication, notional, base amount, ceil division, and fees
- Deterministic SHA-256 order IDs
- Per-market sequence and account command-nonce primitives
- Pure price-time matcher with an ordered maker cursor
- In-memory cursor with the same byte order as the book keys
- Ask, bid, and the other exchange key families, with tests
- Matcher, arithmetic, and key-encoding tests, plus Go benchmarks

**Planned**

- Cosmos SDK v0.54.4 application and CometBFT wiring
- `x/exchange` keeper: book persistence, escrow, settlement, fees
- `x/batch` execution
- Sequencer admission, signatures, ordering, and batch building
- Protobuf queries and CLI

No Cosmos module is wired up yet. `pkg/matching` does not import the SDK.

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
benchmarks          matcher benchmarks
docs                architecture, matching, state layout, ADRs
```

Later milestones add `app`, `x/exchange`, `x/batch`, and `sequencer`. Those directories are not created until they have code.

## Matching example

Ticks, not decimals. A buy of 100 lots at tick 1000 against asks of 30 @ 980, 40 @ 990, and 50 @ 1000 fills 30 @ 980, 40 @ 990, and 30 @ 1000. The incoming remainder is 0. The last maker keeps 20 lots and its original sequence.

A UI can render tick 1000 as `10.00`. That rendering is not part of consensus. See [docs/adr/0001-numeric-model.md](docs/adr/0001-numeric-model.md).

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
