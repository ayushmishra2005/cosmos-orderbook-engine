# Matching engine

`matching.Match` walks one ordered maker cursor. It does not scan a price-level array and it does not search the opposite side of the book by iterating a map.

The cursor for a buy is the ask book, lowest price first. The cursor for a sell is the bid book, highest price first. Within one price, the oldest sequence is first. That order is the book key order documented in [state-layout.md](state-layout.md).

## Rules

- The resting order is the maker. The incoming order is the taker.
- The fill price is the maker's tick.
- A buy matches while `ask <= buy` and stops when `ask > buy`.
- A sell matches while `bid >= sell` and stops when `bid < sell`.
- Equal prices cross.
- Fill quantity is `min(taker remaining, maker remaining)`.
- Matching uses `RemainingQuantity`. `OriginalQuantity` is only an upper bound.
- A maker is fully consumed before the next maker at that price. The only partial maker fill is the last fill, when the taker runs out. That maker keeps its sequence.

`MaxMakerVisits == 0` means the pure function has no cap. The future keeper must pass a non-zero cap. Every peeked maker counts, including an expired maker that is then skipped. When the cap stops the scan, the taker remainder is cancelled. It is not rested: unvisited makers might still cross, and a resting order must not cross the book.

## Self-trade

There is one policy: cancel the taker remainder.

If the next maker that is still inside the limit has the same owner, matching stops. Earlier fills against other owners are kept. The incoming remainder is not rested. The self maker is left on the book.

A maker outside the limit is a normal price-boundary stop, even when the owner matches. The taker may rest. The books do not cross.

FOK is stricter. If self-trade, the visit cap, or a lack of quantity prevents a full fill, the plan has no fills. The simulated partial result is discarded.

## What may rest

`RestIncoming` is true only when all of the following hold:

- the order is a limit GTC or GTD
- remaining quantity is non-zero
- the stop reason is book exhaustion or the price boundary

IOC, FOK, and market orders never rest. Self-trade, the visit cap, and an already-expired incoming GTD cancel the remainder.

## Example

Incoming buy of 100 lots at tick 1000. Resting asks:

| Lots | Tick | Sequence |
| --- | --- | --- |
| 30 | 980 | 1 |
| 40 | 990 | 2 |
| 50 | 1000 | 3 |

Fills, at maker prices:

| Lots | Tick |
| --- | --- |
| 30 | 980 |
| 40 | 990 |
| 30 | 1000 |

Incoming remainder: 0. The last maker still has 20 lots and keeps sequence 3.

## Determinism

The same incoming order, the same ordered maker stream, the same height, and the same visit cap produce the same `MatchPlan`. Fill order is the cursor order. The implementation does not use map iteration, goroutines, wall-clock time, randomness, or floating point.

The plan is not applied here. Settlement, fee charging, and book mutation belong to the future exchange keeper. Fee and notional formulas live in `pkg/arithmetic` so that keeper can call them without a second rounding rule.
