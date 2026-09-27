# ADR 0001: Numeric model

Status: Accepted

Date: 2026-09-27

## Context

An exchange that matches on `float64` prices is not deterministic. Binary floating point cannot represent decimal ticks such as `9.80` exactly, and two implementations can disagree on rounding. Cosmos consensus requires every validator to derive the same fills, fees, and balances.

Bank amounts in a later milestone are integer atoms. The matcher should not convert to atoms on every comparison.

## Decision

Protocol prices are tick counts. Protocol quantities are lot counts. Both are `uint64`.

Each market will define two positive integers:

```text
baseAmount  = quantityLots * baseLotSize
quoteAmount = quantityLots * priceTicks * quoteAtomsPerTickPerLot
```

`baseLotSize` and `quoteAtomsPerTickPerLot` are not stored in this milestone. The arithmetic helpers exist so the keeper has one implementation later.

Example: 5 lots at tick 980 with 100 quote atoms per tick per lot is `5 * 980 * 100 = 490_000` quote atoms. 5 lots with base lot size `1_000_000` is `5_000_000` base atoms. There is no `9.80` in the state machine.

All addition, subtraction, and multiplication used for these values is checked. Overflow and underflow return errors. Integers do not wrap.

Fees use `ceil(notional * numerator / denominator)`. The product is a 128-bit intermediate. Rounding is away from zero so the fee is never short of the rate by a truncated atom. A fee that does not fit in `uint64` is an error.

Division by zero is an error. A zero lot size or a zero quote-atoms-per-tick parameter is an error, not a zero notional.

`float32` and `float64` are not used in protocol packages. Cosmos `math.Int` and `sdk.Dec` are not used in this milestone. Tick and lot products that fit in `uint64` stay in `uint64`. A later keeper can widen a checked `uint64` atom amount into a bank coin. It must not introduce a decimal type on the match path.

## Consequences

Markets choose a tick and a lot at creation and keep them. UI code converts to a display decimal outside consensus. Order IDs, book keys, and fills all carry integer ticks. A match plan from two nodes cannot diverge because of rounding.
