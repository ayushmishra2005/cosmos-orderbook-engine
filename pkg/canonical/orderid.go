package canonical

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

// orderIDDomainSeparator is mixed into every order ID so this hash cannot be
// reused as another protocol digest. Changing it changes every order ID.
const orderIDDomainSeparator = "cosmos-orderbook-engine/order-id/v1"

// OrderIDInput is the preimage of a deterministic order ID.
// ChainID is the canonical chain-id string. ExchangeInstanceID and Owner are
// raw bytes supplied by the caller; this function does not interpret them.
type OrderIDInput struct {
	ChainID            string
	ExchangeInstanceID []byte
	Owner              []byte
	MarketID           domain.MarketID
	CommandNonce       uint64
}

// HashOrderID returns SHA-256 over a length-prefixed preimage:
//
//	u64be len(domain) | domain
//	u64be len(chainID) | chainID
//	u64be len(instance) | instance
//	u64be len(owner) | owner
//	u64be marketID
//	u64be commandNonce
//
// Variable-length fields are length-prefixed. marketID and commandNonce are
// fixed width. The encoding is not JSON and not protobuf.
func HashOrderID(in OrderIDInput) (domain.OrderID, error) {
	preimage, err := orderIDPreimage(in)
	if err != nil {
		return domain.OrderID{}, err
	}
	return sha256.Sum256(preimage), nil
}

func orderIDPreimage(in OrderIDInput) ([]byte, error) {
	if in.ChainID == "" || len(in.ChainID) > maxChainIDLen {
		return nil, ErrInvalidChainID
	}
	if len(in.ExchangeInstanceID) == 0 || len(in.ExchangeInstanceID) > maxInstanceIDLen {
		return nil, ErrInvalidInstanceID
	}
	if err := domain.ValidateOwner(in.Owner); err != nil {
		return nil, err
	}
	if in.MarketID == 0 {
		return nil, domain.ErrInvalidMarket
	}

	dst := make([]byte, 0, 64+len(orderIDDomainSeparator)+len(in.ChainID)+len(in.ExchangeInstanceID)+len(in.Owner))
	dst = appendLenPrefixed(dst, []byte(orderIDDomainSeparator))
	dst = appendLenPrefixed(dst, []byte(in.ChainID))
	dst = appendLenPrefixed(dst, in.ExchangeInstanceID)
	dst = appendLenPrefixed(dst, in.Owner)
	dst = appendU64(dst, uint64(in.MarketID))
	dst = appendU64(dst, in.CommandNonce)
	return dst, nil
}

func appendLenPrefixed(dst, b []byte) []byte {
	dst = appendU64(dst, uint64(len(b)))
	return append(dst, b...)
}

func appendU64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}
