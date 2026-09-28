# State layout

`x/exchange` stores these keys in its KV store. The active-order record is authoritative. Ask and bid values are the 32-byte order ID only.

Integer fields are fixed-width big-endian. Owner bytes use a one-byte length prefix (`1..255`) because keys are scanned lexicographically. That prefix is not the `u64be` length used in the order-ID hash preimage. The two encodings must stay separate.

Prefix `0x00` is unused so a zeroed buffer is not a valid key. Prefixes are consensus-critical.

| Prefix | Key | Value | Iteration |
| --- | --- | --- | --- |
| `0x01` | order ID | order record: fee grosses and a length-prefixed client order id | point lookup |
| `0x02` | marketID \| price \| sequence | order ID | lowest ask, then oldest sequence |
| `0x03` | marketID \| (MaxUint64-price) \| sequence | order ID | highest bid, then oldest sequence |
| `0x04` | ownerLen \| owner \| marketID \| order ID | order ID | one owner's open orders |
| `0x05` | ownerLen \| owner \| clientLen \| client order ID | order ID | one owner's client IDs; not written until a command carries a client ID |
| `0x06` | expiry height \| order ID | order ID | earliest expiry first; GTD rests only |
| `0x07` | ownerLen \| owner \| assetID | available `u64` \| locked `u64` | one owner's balances |
| `0x08` | ownerLen \| owner | last accepted command nonce | point lookup |
| `0x09` | marketID | last order sequence | point lookup |
| `0x0A` | marketID | last trade sequence | point lookup |
| `0x0B` | (no suffix) | exchange revision | singleton |
| `0x0C` | marketID | market record, including the maker and taker fee ppm | point lookup |
| `0x0D` | marketID \| trade sequence | trade record | increasing sequence |
| `0x0E` | assetID | asset record: denom | point lookup |
| `0x0F` | denom | assetID | denom to asset |

An asset record is the codec version, the asset ID, and a one-byte length followed by the bank denom. The denom index value is the asset ID. Deposits resolve that denom before crediting available balance.

The fee schedule is the market's `MakerFeePPM` and `TakerFeePPM`. It is not a second record. Protocol fees accrue in the balance of the reserved owner `exchange/fee-collector` under prefix `0x07`. That owner cannot place orders. Missing balance, nonce, sequence, and revision keys mean zero.

Genesis stores the active-order record, not a second copy of prefixes `0x02` through `0x06`. `InitGenesis` rebuilds those indexes from the orders. The client order id lives on that record and is repeated on the genesis order so the `0x05` index can be rebuilt. Prefix `0x0F` is rebuilt from the asset record. Trades (`0x0D`) are exported so the trade sequence stays tied to a gap-free history. `x/batch` genesis stores the submitter, latest number, head commitment, and finalized batch records.

Ask and bid keys are 25 bytes. The active-order key is 33 bytes. The expiration key is 41 bytes. Market and trade sequence keys are 9 bytes and differ only in the prefix. The revision key is the single byte `0x0B`; the integer lives in the value.

Book keys reject market 0, tick 0, and sequence 0. Expiration height 0 is rejected because GTD expiry 0 is not a height. Asset 0 is rejected. Client order IDs are 1 to 64 bytes.

Decoded owner and client-id slices are copied out of the key. Callers must not alias a reused iterator buffer.

## Why orders are not copied into price-level arrays

A price level is the contiguous key range `marketID | price`. The orders at that level are already adjacent, oldest sequence first. A second structure that stored `[]Order` per price would duplicate the active-order record and could diverge from the book under a partial fill. The book key is the level. The matcher reads that range through a cursor and stops at the first price that does not cross.

`AskMarketPrefix(market)` and `BidMarketPrefix(market)` are the iterator bounds for one market. They are not a generic key framework.

## Bids

Bid keys store `MaxUint64 - price`, implemented as the bitwise complement. Complement is an involution, so decode recovers the original tick. The highest tick complements to `0` and therefore sorts first under ordinary ascending iteration. Sequence is not complemented, so the smaller sequence still sorts first at that price.

Asks store the raw tick. The lowest ask sorts first for the same reason, without a complement.

## x/batch

`x/batch` uses its own module store. These prefixes are independent of `x/exchange`. A batch record stores the batch number, batch ID, previous commitment, batch commitment, results hash, execution height, pre and post exchange revisions, command count, and a per-command summary. It does not copy the book.

| Prefix | Key | Value |
| --- | --- | --- |
| `0x01` | (none) | authorized submitter, latest batch number, and head commitment |
| `0x02` | batch number | batch record |
| `0x03` | batch ID | batch number |

Both values use codec version 2. The head commitment is 32 bytes. Before batch 1 it is 32 zero bytes, and batch 1 must name that value as its previous commitment. The batch record stores that previous commitment, its own `BatchCommitment`, and the `ResultsHash` beside the command summaries. The order book is not stored again.
