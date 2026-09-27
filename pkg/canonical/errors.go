package canonical

import "errors"

var (
	ErrInvalidKey           = errors.New("canonical: invalid key")
	ErrInvalidChainID       = errors.New("canonical: invalid chain id")
	ErrInvalidInstanceID    = errors.New("canonical: invalid exchange instance id")
	ErrInvalidClientOrderID = errors.New("canonical: invalid client order id")
)

const (
	// Key prefixes are consensus-critical. Zero is unused so a zeroed buffer
	// is never a valid key. Do not renumber a prefix once state exists.
	PrefixActiveOrder       byte = 0x01
	PrefixAskBook           byte = 0x02
	PrefixBidBook           byte = 0x03
	PrefixOwnerOpenOrder    byte = 0x04
	PrefixActiveClientOrder byte = 0x05
	PrefixExpiration        byte = 0x06
	PrefixBalance           byte = 0x07
	PrefixAccountNonce      byte = 0x08
	PrefixMarketSequence    byte = 0x09
	PrefixTradeSequence     byte = 0x0A
	PrefixExchangeRevision  byte = 0x0B
	PrefixMarket            byte = 0x0C
	PrefixTrade             byte = 0x0D
	PrefixAsset             byte = 0x0E
	PrefixAssetDenom        byte = 0x0F
)

// MaxClientOrderIDLength bounds the client order id stored in the active-id index.
const MaxClientOrderIDLength = 64

const (
	maxChainIDLen    = 128
	maxInstanceIDLen = 32
	// MaxDenomLength bounds a bank denom stored in the asset index.
	MaxDenomLength = 128
)
