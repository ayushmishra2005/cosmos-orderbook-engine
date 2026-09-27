package canonical

import (
	"encoding/binary"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

// Owner length in state keys is a uint8, not the uint64 used in the order-ID
// preimage. Key order is a lexicographic scan; the hash preimage is not.
// Callers must not unify the two encodings.

func appendOwner(dst, owner []byte) ([]byte, error) {
	if err := domain.ValidateOwner(owner); err != nil {
		return nil, err
	}
	dst = append(dst, byte(len(owner)))
	dst = append(dst, owner...)
	return dst, nil
}

func appendClientID(dst, id []byte) ([]byte, error) {
	if len(id) == 0 || len(id) > MaxClientOrderIDLength {
		return nil, ErrInvalidClientOrderID
	}
	dst = append(dst, byte(len(id)))
	dst = append(dst, id...)
	return dst, nil
}

// readPrefixed copies the value out of key so the caller does not alias a
// reused KV iterator buffer.
func readPrefixed(key []byte, i, max int) ([]byte, int, error) {
	if i >= len(key) {
		return nil, 0, ErrInvalidKey
	}
	n := int(key[i])
	i++
	if n == 0 || n > max || len(key)-i < n {
		return nil, 0, ErrInvalidKey
	}
	out := make([]byte, n)
	copy(out, key[i:i+n])
	return out, i + n, nil
}

func readU64(key []byte, i int) (uint64, int, error) {
	if len(key)-i < 8 {
		return 0, 0, ErrInvalidKey
	}
	return binary.BigEndian.Uint64(key[i : i+8]), i + 8, nil
}

func requireConsumed(key []byte, i int) error {
	if i != len(key) {
		return ErrInvalidKey
	}
	return nil
}

func encodeMarketPrefixed(prefix byte, marketID domain.MarketID) ([]byte, error) {
	if marketID == 0 {
		return nil, domain.ErrInvalidMarket
	}
	var key [9]byte
	key[0] = prefix
	binary.BigEndian.PutUint64(key[1:], uint64(marketID))
	return key[:], nil
}

func decodeMarketPrefixed(prefix byte, key []byte) (domain.MarketID, error) {
	if len(key) != 9 || key[0] != prefix {
		return 0, ErrInvalidKey
	}
	id := domain.MarketID(binary.BigEndian.Uint64(key[1:]))
	if id == 0 {
		return 0, domain.ErrInvalidMarket
	}
	return id, nil
}

// EncodeActiveOrderKey addresses the full order record by order ID.
func EncodeActiveOrderKey(id domain.OrderID) ([]byte, error) {
	if id.IsZero() {
		return nil, domain.ErrInvalidOrderID
	}
	key := make([]byte, 1+len(id))
	key[0] = PrefixActiveOrder
	copy(key[1:], id[:])
	return key, nil
}

// DecodeActiveOrderKey reverses EncodeActiveOrderKey.
func DecodeActiveOrderKey(key []byte) (domain.OrderID, error) {
	if len(key) != 1+32 || key[0] != PrefixActiveOrder {
		return domain.OrderID{}, ErrInvalidKey
	}
	var id domain.OrderID
	copy(id[:], key[1:])
	if id.IsZero() {
		return domain.OrderID{}, domain.ErrInvalidOrderID
	}
	return id, nil
}

// EncodeOwnerOpenOrderKey indexes a live order under its owner.
// Within one owner, byte order is marketID then order ID. This index is not
// the matching book.
func EncodeOwnerOpenOrderKey(owner []byte, marketID domain.MarketID, id domain.OrderID) ([]byte, error) {
	if marketID == 0 {
		return nil, domain.ErrInvalidMarket
	}
	if id.IsZero() {
		return nil, domain.ErrInvalidOrderID
	}
	dst, err := appendOwner([]byte{PrefixOwnerOpenOrder}, owner)
	if err != nil {
		return nil, err
	}
	dst = appendU64(dst, uint64(marketID))
	dst = append(dst, id[:]...)
	return dst, nil
}

// DecodeOwnerOpenOrderKey reverses EncodeOwnerOpenOrderKey.
func DecodeOwnerOpenOrderKey(key []byte) ([]byte, domain.MarketID, domain.OrderID, error) {
	if len(key) == 0 || key[0] != PrefixOwnerOpenOrder {
		return nil, 0, domain.OrderID{}, ErrInvalidKey
	}
	owner, i, err := readPrefixed(key, 1, domain.MaxOwnerLength)
	if err != nil {
		return nil, 0, domain.OrderID{}, err
	}
	marketWord, i, err := readU64(key, i)
	if err != nil {
		return nil, 0, domain.OrderID{}, err
	}
	if len(key)-i != 32 {
		return nil, 0, domain.OrderID{}, ErrInvalidKey
	}
	var id domain.OrderID
	copy(id[:], key[i:])
	marketID := domain.MarketID(marketWord)
	if marketID == 0 {
		return nil, 0, domain.OrderID{}, domain.ErrInvalidMarket
	}
	if id.IsZero() {
		return nil, 0, domain.OrderID{}, domain.ErrInvalidOrderID
	}
	return owner, marketID, id, nil
}

// OwnerOpenOrderPrefix iterates every open order for one owner.
func OwnerOpenOrderPrefix(owner []byte) ([]byte, error) {
	return appendOwner([]byte{PrefixOwnerOpenOrder}, owner)
}

// EncodeActiveClientOrderKey indexes an owner's live client order id.
func EncodeActiveClientOrderKey(owner, clientOrderID []byte) ([]byte, error) {
	dst, err := appendOwner([]byte{PrefixActiveClientOrder}, owner)
	if err != nil {
		return nil, err
	}
	return appendClientID(dst, clientOrderID)
}

// DecodeActiveClientOrderKey reverses EncodeActiveClientOrderKey.
func DecodeActiveClientOrderKey(key []byte) ([]byte, []byte, error) {
	if len(key) == 0 || key[0] != PrefixActiveClientOrder {
		return nil, nil, ErrInvalidKey
	}
	owner, i, err := readPrefixed(key, 1, domain.MaxOwnerLength)
	if err != nil {
		return nil, nil, err
	}
	clientID, i, err := readPrefixed(key, i, MaxClientOrderIDLength)
	if err != nil {
		return nil, nil, err
	}
	if err := requireConsumed(key, i); err != nil {
		return nil, nil, err
	}
	return owner, clientID, nil
}

// ActiveClientOrderPrefix iterates client order ids for one owner.
func ActiveClientOrderPrefix(owner []byte) ([]byte, error) {
	return appendOwner([]byte{PrefixActiveClientOrder}, owner)
}

// EncodeExpirationKey orders GTD orders by the first height at which they
// are expired, then by order ID. Ascending iteration is the earliest expiry.
func EncodeExpirationKey(height uint64, id domain.OrderID) ([]byte, error) {
	if height == 0 {
		return nil, domain.ErrInvalidExpiry
	}
	if id.IsZero() {
		return nil, domain.ErrInvalidOrderID
	}
	var key [1 + 8 + 32]byte
	key[0] = PrefixExpiration
	binary.BigEndian.PutUint64(key[1:9], height)
	copy(key[9:], id[:])
	return key[:], nil
}

// DecodeExpirationKey reverses EncodeExpirationKey.
func DecodeExpirationKey(key []byte) (uint64, domain.OrderID, error) {
	if len(key) != 1+8+32 || key[0] != PrefixExpiration {
		return 0, domain.OrderID{}, ErrInvalidKey
	}
	height := binary.BigEndian.Uint64(key[1:9])
	var id domain.OrderID
	copy(id[:], key[9:])
	if height == 0 {
		return 0, domain.OrderID{}, domain.ErrInvalidExpiry
	}
	if id.IsZero() {
		return 0, domain.OrderID{}, domain.ErrInvalidOrderID
	}
	return height, id, nil
}

// EncodeBalanceKey addresses escrow for one owner and asset, in atoms.
func EncodeBalanceKey(owner []byte, assetID domain.AssetID) ([]byte, error) {
	if assetID == 0 {
		return nil, domain.ErrInvalidAsset
	}
	dst, err := appendOwner([]byte{PrefixBalance}, owner)
	if err != nil {
		return nil, err
	}
	return appendU64(dst, uint64(assetID)), nil
}

// DecodeBalanceKey reverses EncodeBalanceKey.
func DecodeBalanceKey(key []byte) ([]byte, domain.AssetID, error) {
	if len(key) == 0 || key[0] != PrefixBalance {
		return nil, 0, ErrInvalidKey
	}
	owner, i, err := readPrefixed(key, 1, domain.MaxOwnerLength)
	if err != nil {
		return nil, 0, err
	}
	word, i, err := readU64(key, i)
	if err != nil {
		return nil, 0, err
	}
	if err := requireConsumed(key, i); err != nil {
		return nil, 0, err
	}
	assetID := domain.AssetID(word)
	if assetID == 0 {
		return nil, 0, domain.ErrInvalidAsset
	}
	return owner, assetID, nil
}

// BalanceOwnerPrefix iterates balances for one owner.
func BalanceOwnerPrefix(owner []byte) ([]byte, error) {
	return appendOwner([]byte{PrefixBalance}, owner)
}

// EncodeAccountNonceKey addresses the last accepted command nonce of an account.
func EncodeAccountNonceKey(owner []byte) ([]byte, error) {
	return appendOwner([]byte{PrefixAccountNonce}, owner)
}

// DecodeAccountNonceKey reverses EncodeAccountNonceKey.
func DecodeAccountNonceKey(key []byte) ([]byte, error) {
	if len(key) == 0 || key[0] != PrefixAccountNonce {
		return nil, ErrInvalidKey
	}
	owner, i, err := readPrefixed(key, 1, domain.MaxOwnerLength)
	if err != nil {
		return nil, err
	}
	if err := requireConsumed(key, i); err != nil {
		return nil, err
	}
	return owner, nil
}

// EncodeMarketSequenceKey addresses the last assigned order sequence of a market.
func EncodeMarketSequenceKey(marketID domain.MarketID) ([]byte, error) {
	return encodeMarketPrefixed(PrefixMarketSequence, marketID)
}

// DecodeMarketSequenceKey reverses EncodeMarketSequenceKey.
func DecodeMarketSequenceKey(key []byte) (domain.MarketID, error) {
	return decodeMarketPrefixed(PrefixMarketSequence, key)
}

// EncodeTradeSequenceKey addresses the last assigned trade sequence of a market.
func EncodeTradeSequenceKey(marketID domain.MarketID) ([]byte, error) {
	return encodeMarketPrefixed(PrefixTradeSequence, marketID)
}

// DecodeTradeSequenceKey reverses EncodeTradeSequenceKey.
func DecodeTradeSequenceKey(key []byte) (domain.MarketID, error) {
	return decodeMarketPrefixed(PrefixTradeSequence, key)
}

// EncodeExchangeRevisionKey is the singleton key for the exchange parameter revision.
// The revision integer is the stored value, not part of the key.
func EncodeExchangeRevisionKey() []byte {
	return []byte{PrefixExchangeRevision}
}

// DecodeExchangeRevisionKey accepts only the singleton revision key.
func DecodeExchangeRevisionKey(key []byte) error {
	if len(key) != 1 || key[0] != PrefixExchangeRevision {
		return ErrInvalidKey
	}
	return nil
}

// EncodeMarketKey addresses one market record.
func EncodeMarketKey(marketID domain.MarketID) ([]byte, error) {
	return encodeMarketPrefixed(PrefixMarket, marketID)
}

// DecodeMarketKey reverses EncodeMarketKey.
func DecodeMarketKey(key []byte) (domain.MarketID, error) {
	return decodeMarketPrefixed(PrefixMarket, key)
}

// EncodeTradeKey addresses one trade. Sequence 0 is not a trade.
func EncodeTradeKey(marketID domain.MarketID, sequence uint64) ([]byte, error) {
	if sequence == 0 {
		return nil, domain.ErrInvalidSequence
	}
	key, err := encodeMarketPrefixed(PrefixTrade, marketID)
	if err != nil {
		return nil, err
	}
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], sequence)
	return append(key, seq[:]...), nil
}

// DecodeTradeKey reverses EncodeTradeKey.
func DecodeTradeKey(key []byte) (domain.MarketID, uint64, error) {
	if len(key) != 17 || key[0] != PrefixTrade {
		return 0, 0, ErrInvalidKey
	}
	marketID, err := decodeMarketPrefixed(PrefixTrade, key[:9])
	if err != nil {
		return 0, 0, err
	}
	sequence := binary.BigEndian.Uint64(key[9:])
	if sequence == 0 {
		return 0, 0, domain.ErrInvalidSequence
	}
	return marketID, sequence, nil
}
