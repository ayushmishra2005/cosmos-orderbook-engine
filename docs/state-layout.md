# State layout

Keys below are the byte layout the future `x/exchange` keeper will store. This milestone implements the codecs and tests. It does not open a KV store.

Integer fields are fixed-width big-endian. Owner bytes use a one-byte length prefix (`1..255`) because keys are scanned lexicographically. That prefix is not the `u64be` length used in the order-ID hash preimage. The two encodings must stay separate.

Prefix `0x00` is unused so a zeroed buffer is not a valid key. Prefixes are consensus-critical.

| Prefix | Key | Value (later) | Iteration |
| --- | --- | --- | --- |
| `0x01` | order ID | `domain.Order` | point lookup |
| `0x02` | marketID \| price \| sequence | order ID | lowest ask, then oldest sequence |
| `0x03` | marketID \| (MaxUint64-price) \| sequence | order ID | highest bid, then oldest sequence |
| `0x04` | ownerLen \| owner \| marketID \| order ID | empty or duplicate ID | one owner's open orders |
| `0x05` | ownerLen \| owner \| clientLen \| client order ID | order ID | one owner's client IDs |
| `0x06` | expiry height \| order ID | order ID | earliest expiry first |
| `0x07` | ownerLen \| owner \| assetID | balance in atoms | one owner's balances |
| `0x08` | ownerLen \| owner | last accepted command nonce | point lookup |
| `0x09` | marketID | last order sequence | point lookup |
| `0x0A` | marketID | last trade sequence | point lookup |
| `0x0B` | (no suffix) | exchange revision | singleton |

Ask and bid keys are 25 bytes. The active-order key is 33 bytes. The expiration key is 41 bytes. Market and trade sequence keys are 9 bytes and differ only in the prefix. The revision key is the single byte `0x0B`; the integer lives in the value.

Book keys reject market 0, tick 0, and sequence 0. Expiration height 0 is rejected because GTD expiry 0 is not a height. Asset 0 is rejected. Client order IDs are 1 to 64 bytes.

Decoded owner and client-id slices are copied out of the key. Callers must not alias a reused iterator buffer.

## Why orders are not copied into price-level arrays

A price level is the contiguous key range `marketID | price`. The orders at that level are already adjacent, oldest sequence first. A second structure that stored `[]Order` per price would duplicate the active-order record and could diverge from the book under a partial fill. The book key is the level. The matcher reads that range through a cursor and stops at the first price that does not cross.

`AskMarketPrefix(market)` and `BidMarketPrefix(market)` are the iterator bounds for one market. They are not a generic key framework.

## Bids

Bid keys store `MaxUint64 - price`, implemented as the bitwise complement. Complement is an involution, so decode recovers the original tick. The highest tick complements to `0` and therefore sorts first under ordinary ascending iteration. Sequence is not complemented, so the smaller sequence still sorts first at that price.

Asks store the raw tick. The lowest ask sorts first for the same reason, without a complement.
