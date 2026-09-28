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

**Implemented — milestone 5, batch commitments**

- `ResultsHash` commits the ordered command results
- `BatchCommitment` chains each finalized batch to the previous commitment
- Batch 1 names 32 zero bytes as the previous commitment
- A mismatched previous commitment is rejected and leaves the head unchanged
- The commitment binds the batch, its results, the revisions, and the execution height. It is not an exchange state root, a validity proof, or a fraud proof

**Implemented — milestone 6, off-chain sequencer**

- `orderbook-sequencer` admits owner-signed place and cancel commands
- One writer assigns a monotonic position and appends it to a local journal before admission succeeds
- The oldest pending commands become `MsgFinalizeBatch` and are signed by the configured submitter
- A command is finalized only after the chain includes the batch
- Admission is provisional. It is not execution and it is not chain acceptance

**Implemented — milestone 7, observability and benchmarks**

- Prometheus text on the sequencer `GET /metrics` endpoint
- Exchange and batch execution counters that cannot change a committed result
- Matcher, order-book storage, batch-execution, and journal benchmarks
- CPU and heap profiles through `go test -cpuprofile` and `-memprofile`
- Bounded retry delay after a failed batch broadcast

**Implemented — milestone 8, multi-validator checks**

- `scripts/localnet4.sh` starts four validators from one genesis
- Test digests of exchange and batch state. They are not consensus commitments
- Explicit exchange, custody, and batch invariant checks
- Failure injection, replay, property, and resource-limit tests

**Implemented — milestone 9, genesis round trip**

- Resting orders, locked balances, fee grosses, sequences, and trades export and import
- Book, owner, client, and expiration indexes are rebuilt during genesis init
- The batch commitment head is preserved, so the next batch extends the same chain

**Planned**

- Websocket feeds

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
pkg/canonical       order IDs, state keys, batch commands, results, commitments
pkg/matching        pure matcher
pkg/matching/memsource
x/exchange          keeper, module, messages, queries
x/batch             ordered batch execution
app                 chain wiring
cmd/cosmos-orderbookd
cmd/orderbook-sequencer
sequencer           admission, journal, batch building, chain submission
benchmarks          matcher benchmarks
internal/telemetry  Prometheus recorders
docs                architecture, matching, state layout
```

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

Four validators use one genesis. Home defaults to `.localnet4`. RPC ports are `26657`, `26667`, `26677`, and `26687`. gRPC starts at `29290` and each later node adds 10.

```bash
./scripts/localnet4.sh
./scripts/localnet4.sh stop
./scripts/localnet4.sh reset
./scripts/localnet4.sh smoke
```

`smoke` deposits, matches one trade, and checks that the four nodes report the same exchange state. No batch is submitted, so each node reports the same empty batch head.

## Sequencer

`orderbook-sequencer` is off-chain. Validators still execute every command. `POST /v1/commands` returns `provisional: true` after the command is checked and written to the journal. That response does not mean the order executed or that the chain accepted it.

The process reads the latest batch commitment and exchange revision, builds `MsgFinalizeBatch` from the oldest pending positions, and broadcasts it with the Cosmos SDK keyring. The key is not stored in a config file. Commands stay pending if the transaction fails. A nonce that the chain has already consumed is dropped on the next attempt. Finalized commands are not submitted again after a restart.

```bash
make build
./scripts/localnet.sh
```

In another shell, after the node is producing blocks:

```bash
./build/orderbook-sequencer --home .localnet --from submitter --chain-id orderbook-1
```

`--node` defaults to `http://127.0.0.1:26657`. The same values can be set with `ORDERBOOK_NODE`, `ORDERBOOK_CHAIN_ID`, `ORDERBOOK_INSTANCE_ID`, `ORDERBOOK_JOURNAL`, `ORDERBOOK_LISTEN`, `ORDERBOOK_FROM`, `ORDERBOOK_KEYRING_BACKEND`, `ORDERBOOK_HOME`, `ORDERBOOK_MAX_BATCH`, `ORDERBOOK_BATCH_INTERVAL`, `ORDERBOOK_RETRY_INITIAL`, `ORDERBOOK_RETRY_MAX`, `ORDERBOOK_FEES`, and `ORDERBOOK_GAS`.

`POST /v1/commands` takes one JSON command. `chain_id`, `exchange_instance_id`, and `owner` are text. `pub_key`, `signature`, and `cancel.order_id` are hex. The signature is over the canonical batch-command bytes, not over this JSON. `GET /health` reports pending and in-flight counts, the latest observed batch, and chain connectivity. `GET /metrics` is Prometheus text. `GET /v1/pending` lists commands that are not yet finalized.

A failed broadcast waits `--retry-initial` (default 1s) before the next attempt. The wait doubles up to `--retry-max` (default 30s) and resets after a batch is included. The wait does not reorder commands. A command whose nonce the chain has already passed is dropped. Other commands, including an order rejected for insufficient balance, stay pending.

## Tests

```bash
go test ./...
go test -race ./...
go vet ./...
```

`make check` runs all three. Fuzz targets are included; `go test` executes their seed corpus. A longer run is `go test -fuzz=FuzzMatchReplay -fuzztime=10s ./pkg/matching`.

## Benchmarks

```bash
make bench-matching   # pure matcher
make bench-storage    # Cosmos-backed order book
make bench-batch      # keeper execution of 100, 1,000, and 10,000 commands
make bench-journal    # append, including fsync, and replay
make bench
```

`make bench-count` repeats each benchmark six times. That output is input for `benchstat`. CPU and heap profiles:

```bash
make profile-cpu
make profile-heap
go tool pprof -top cpu.out
go tool pprof -top mem.out
```

The harness prints `ns/op`, `B/op`, and `allocs/op`. Matcher benchmarks also print `makers/op` and `fills/op`. Storage and batch benchmarks also print `kv_reads/op` and `kv_writes/op`. A batch larger than 128 commands is several atomic `FinalizeBatch` calls. None of these numbers are committed block throughput. Profile files (`cpu.out`, `mem.out`) are not source.

One measurement on this machine, Go 1.27.1, darwin/arm64, Apple M5 Max:

| workload | result |
| --- | --- |
| matcher, one fill | 70.67 ns/op, 104 B/op, 2 allocs/op |
| matcher, 1,000 same-price makers | 52.0 µs/op, 198,256 B/op, 1,011 allocs/op |
| matcher, 10,000 same-price makers | 652 µs/op, 3,813,942 B/op, 10,019 allocs/op |
| matcher, best price does not cross | 56.52 ns/op |
| Cosmos book lookup | 330 ns/op, 1 KV read |
| Cosmos book, match 100 makers | 987 µs/op, 313 KV reads, 407 KV writes |
| batch keeper, 10,000 resting places, one sample | 1.06 s, 120,159 KV reads, 80,000 KV writes |
| journal append, one record, with fsync | 3.44 ms/op |
| journal frame encode, no fsync | 111 ns/op |

## License

Apache-2.0. See [LICENSE](LICENSE).
