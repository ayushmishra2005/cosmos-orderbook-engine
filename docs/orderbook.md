# Order book

Orders are domain values, not protobuf messages. A later transaction decoder maps wire messages onto `domain.Order` before anything reaches the matcher.

## Identifiers

`OrderID` is 32 bytes:

```text
SHA-256(
    u64be len(domain) | domain
    u64be len(chainID) | chainID
    u64be len(instance) | instance
    u64be len(owner) | owner
    u64be marketID
    u64be commandNonce
)
```

The domain separator is `cosmos-orderbook-engine/order-id/v1`. Variable-length fields are length-prefixed so `chainID="ab"` with instance `c` does not collide with `chainID="a"` and instance `bc`. The encoding is not JSON and not protobuf.

`owner` is canonical address bytes. Bech32 text is not hashed.

The same inputs always produce the same ID. The keeper must still reject a command whose nonce is not `ExpectedCommandNonce(lastAccepted)`. `HashOrderID` does not check that sequence; it only hashes the bytes it is given. The first accepted nonce is 1. Nonce 0 means none have been accepted.

## Sequence

Each market has one order sequence. `NextSequence(0)` is 1. The next accepted resting order receives `last+1`. Overflow is an error; the counter does not wrap.

A lower sequence is older. At the same price the older order is the maker that trades first. A partial fill does not allocate a new sequence, so the remainder keeps its place in the book.

Sequence 0 is not a book position. Incoming orders may still have sequence 0. The keeper assigns a sequence only if the match plan says the order rests.

## Prices and quantities

`Price` is a tick count. `Quantity` is a lot count. Display strings such as `10.00` are not protocol values. The worked example uses ticks `980`, `990`, and `1000` for what a UI would show as `9.80`, `9.90`, and `10.00`.

Tick 0 is invalid. Market orders still carry a worst acceptable tick and never rest.

## Time in force

| Value | Remainder |
| --- | --- |
| GTC | May rest. No expiry height. |
| GTD | May rest while `height < ExpiryHeight`. `ExpiryHeight` is the first height at which the order is expired. |
| IOC | Never rests. Unfilled quantity is cancelled. |
| FOK | Executes entirely or not at all. Never rests. |

Market orders are IOC or FOK. A market GTC is rejected.

GTD makers at or past `ExpiryHeight` are not eligible. The matcher skips them. The plan does not list those skips; deleting expired keys is keeper work, using the expiration index in the state layout.
