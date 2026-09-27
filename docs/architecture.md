# Architecture

`cosmos-orderbook-engine` is a Cosmos SDK exchange. This repository currently contains the deterministic core only. The Cosmos application, keepers, sequencer, and batch execution are not implemented.

The target stack for later milestones is Go 1.26, Cosmos SDK v0.54.4, and a CometBFT release compatible with that SDK. Those modules are intentionally not dependencies yet. The matcher must not import the SDK.

## Packages

```text
pkg/domain
    ↓
pkg/arithmetic
    ↓
pkg/matching
    ↓
x/exchange          (not implemented)
    ↓
x/batch             (not implemented)
    ↓
app                 (not implemented)
```

`pkg/canonical` depends only on `pkg/domain`. It encodes order IDs and the state keys the future `x/exchange` keeper will use.

`pkg/matching` depends on `pkg/domain` and `pkg/arithmetic`. It does not import `pkg/canonical`. Book order is the cursor's responsibility. The in-memory cursor used by tests sorts with the canonical book keys, which is the same byte order a Cosmos iterator will produce.

Forbidden edges:

```text
pkg/* -> x/*
pkg/* -> app/*
x/exchange -> x/batch
```

There is no `x/orderbook` or `x/settlement` module. Both will live inside `x/exchange`.

## Why matching is separate from state

`Match` is a pure function of an incoming order, a forward cursor of makers, a block height, and a visit cap. It returns a plan. It does not write balances, delete orders, or assign sequences.

That split keeps consensus rules testable without a chain and keeps SDK types out of the hot path. A later keeper opens a prefix iterator, passes it to `Match` as an `OrderSource`, and applies the plan. Replaying the same cursor and the same input produces the same plan.

## What this milestone does not do

Bank custody, deposits, withdrawals, fee collection, settlement, sequencer networking, signed batches, queries, and the CometBFT app are planned. They are not stubbed in as empty modules.
