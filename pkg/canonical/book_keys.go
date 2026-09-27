package canonical

import (
	"encoding/binary"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

// A one-byte owner length is consensus state. This does not compile if
// domain.MaxOwnerLength no longer fits in that prefix.
const _ = uint8(domain.MaxOwnerLength)

const bookKeyLen = 1 + 8 + 8 + 8

func validateBookKey(marketID domain.MarketID, price domain.Price, sequence domain.Sequence) error {
	if marketID == 0 {
		return domain.ErrInvalidMarket
	}
	if price == 0 {
		return domain.ErrInvalidPrice
	}
	if sequence == 0 {
		return domain.ErrInvalidSequence
	}
	return nil
}

// complementPrice reverses bid order. It is an involution:
// complement(complement(p)) == p. The highest price complements to 0, so
// ascending byte order yields the best bid first.
func complementPrice(price domain.Price) uint64 {
	return ^uint64(price)
}

func encodeBookKey(prefix byte, marketID domain.MarketID, priceWord uint64, sequence domain.Sequence) []byte {
	var key [bookKeyLen]byte
	key[0] = prefix
	binary.BigEndian.PutUint64(key[1:9], uint64(marketID))
	binary.BigEndian.PutUint64(key[9:17], priceWord)
	binary.BigEndian.PutUint64(key[17:25], uint64(sequence))
	return key[:]
}

func decodeBookKey(prefix byte, key []byte) (domain.MarketID, uint64, domain.Sequence, error) {
	if len(key) != bookKeyLen || key[0] != prefix {
		return 0, 0, 0, ErrInvalidKey
	}
	marketID := domain.MarketID(binary.BigEndian.Uint64(key[1:9]))
	word := binary.BigEndian.Uint64(key[9:17])
	sequence := domain.Sequence(binary.BigEndian.Uint64(key[17:25]))
	return marketID, word, sequence, nil
}

func marketPrefix(prefix byte, marketID domain.MarketID) ([]byte, error) {
	if marketID == 0 {
		return nil, domain.ErrInvalidMarket
	}
	var key [9]byte
	key[0] = prefix
	binary.BigEndian.PutUint64(key[1:], uint64(marketID))
	return key[:], nil
}

// EncodeAskKey encodes prefix | marketID | price | sequence.
// Ascending byte order is the lowest ask first, then the oldest sequence.
func EncodeAskKey(marketID domain.MarketID, price domain.Price, sequence domain.Sequence) ([]byte, error) {
	if err := validateBookKey(marketID, price, sequence); err != nil {
		return nil, err
	}
	return encodeBookKey(PrefixAskBook, marketID, uint64(price), sequence), nil
}

// DecodeAskKey reverses EncodeAskKey.
func DecodeAskKey(key []byte) (domain.MarketID, domain.Price, domain.Sequence, error) {
	marketID, word, sequence, err := decodeBookKey(PrefixAskBook, key)
	if err != nil {
		return 0, 0, 0, err
	}
	price := domain.Price(word)
	if err := validateBookKey(marketID, price, sequence); err != nil {
		return 0, 0, 0, err
	}
	return marketID, price, sequence, nil
}

// EncodeBidKey encodes prefix | marketID | descendingPrice | sequence.
// descendingPrice = MaxUint64 - price, so ascending byte order is the
// highest bid first, then the oldest sequence.
func EncodeBidKey(marketID domain.MarketID, price domain.Price, sequence domain.Sequence) ([]byte, error) {
	if err := validateBookKey(marketID, price, sequence); err != nil {
		return nil, err
	}
	return encodeBookKey(PrefixBidBook, marketID, complementPrice(price), sequence), nil
}

// DecodeBidKey reverses EncodeBidKey and returns the original price.
func DecodeBidKey(key []byte) (domain.MarketID, domain.Price, domain.Sequence, error) {
	marketID, word, sequence, err := decodeBookKey(PrefixBidBook, key)
	if err != nil {
		return 0, 0, 0, err
	}
	price := domain.Price(complementPrice(domain.Price(word)))
	if err := validateBookKey(marketID, price, sequence); err != nil {
		return 0, 0, 0, err
	}
	return marketID, price, sequence, nil
}

// AskMarketPrefix is the iterator prefix for one market's ask book.
func AskMarketPrefix(marketID domain.MarketID) ([]byte, error) {
	return marketPrefix(PrefixAskBook, marketID)
}

// BidMarketPrefix is the iterator prefix for one market's bid book.
func BidMarketPrefix(marketID domain.MarketID) ([]byte, error) {
	return marketPrefix(PrefixBidBook, marketID)
}
