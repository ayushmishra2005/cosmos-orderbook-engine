# ADR 0002: Order book key layout

Status: Accepted

Date: 2026-09-27

## Context

Cosmos collections iterate keys in ascending byte order. The matcher needs the best price first and, at that price, the oldest order first. Loading every order into a slice and sorting it inside consensus would scan the whole book and would depend on sort stability. A map from price to orders has no iteration order.

The book also has to support a bounded scan: stop at the first price that does not cross, and stop after a visit cap.

## Decision

Resting orders are individual keys, not documents inside a price-level array.

```text
Ask: prefix | marketID | price | sequence
Bid: prefix | marketID | (MaxUint64 - price) | sequence
```

All three integers are 8-byte big-endian. `marketID` is first so one market is one prefix. `sequence` is last and is not complemented, so the smaller sequence is first at a fixed price. Sequence numbers start at 1 and only increase (`NextSequence`). Older orders therefore sort first without a second index.

Bids store the complement of the price. Forward iteration then yields the highest bid first. Asks store the raw price, so forward iteration yields the lowest ask first. Decode of a bid key complements again. Complement is an involution, including at `MaxUint64` (stored as zero) and at tick 1 (stored as `MaxUint64-1`).

Tick 0, market 0, and sequence 0 are not valid book keys.

The matcher never sees these bytes. It consumes an `OrderSource` that must already be in this order. The in-memory test source sorts by the encoded key and rejects duplicate keys, so there is only one order consistent with byte comparison. The production iterator will be a prefix scan of the same encoding. Those two cursors are what differential tests should compare later.

Other key families (active order, owner index, client order id, expiration, balance, nonces, sequences, revision) use distinct prefixes. They are lookup indexes. They are not walked by the matcher. Layout and prefix bytes are listed in [state-layout.md](../state-layout.md).

## Consequences

A partial fill updates the order record and leaves the book key in place, because price and sequence do not change. Priority is preserved without a delete and reinsert.

Changing a prefix or the complement rule changes key order and is a consensus break. Prefixes start at `0x01` and are fixed in `pkg/canonical`.

The matcher can stop at the first key that does not cross. It does not allocate a price-level slice and it does not read orders behind the spread, except when a visit cap or an expired maker forces it to read and then skip.
