package canonical

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const (
	// BatchCommandDomain separates batch-command signatures from order IDs
	// and from the batch identity hash.
	BatchCommandDomain = "cosmos-orderbook/batch-command/v1"
	// BatchIDDomain separates a batch identity from a command signature.
	// The digest is not an exchange state root.
	BatchIDDomain = "cosmos-orderbook/batch-id/v1"
	// BatchCommandVersion is the only command protocol version this encoder accepts.
	BatchCommandVersion uint32 = 1
)

// CommandType is the explicit command discriminator in the signing bytes.
type CommandType uint8

const (
	CommandTypePlace  CommandType = 1
	CommandTypeCancel CommandType = 2
)

// Place is the signed body of a place-order command.
type Place struct {
	MarketID      domain.MarketID
	Side          domain.Side
	Type          domain.OrderType
	TimeInForce   domain.TimeInForce
	Quantity      domain.Quantity
	Price         domain.Price
	ExpiryHeight  uint64
	ClientOrderID []byte
}

// Cancel is the signed body of a cancel-order command.
type Cancel struct {
	OrderID domain.OrderID
}

// Command is one trader-signed batch command.
// PubKey and Signature are not part of the signing bytes.
type Command struct {
	ProtocolVersion    uint32
	ChainID            string
	ExchangeInstanceID []byte
	Owner              []byte
	Nonce              uint64
	Type               CommandType
	Place              *Place
	Cancel             *Cancel
	PubKey             []byte
	Signature          []byte
}

// CommandSignBytes encodes the command that a trader signs.
//
//	u64be len(domain) | domain
//	u32be protocolVersion
//	u64be len(chainID) | chainID
//	u64be len(instance) | instance
//	u64be len(owner) | owner
//	u64be commandNonce
//	u8 commandType
//	place or cancel body
//
// Place body: market, side, order type, time in force, quantity, price,
// expiry, then a length-prefixed client order id. Cancel body: 32-byte order ID.
// Integers are big-endian. The encoding is not JSON and not protobuf.
func CommandSignBytes(cmd Command) ([]byte, error) {
	if cmd.ProtocolVersion != BatchCommandVersion {
		return nil, ErrUnsupportedVersion
	}
	if cmd.ChainID == "" || len(cmd.ChainID) > maxChainIDLen {
		return nil, ErrInvalidChainID
	}
	if len(cmd.ExchangeInstanceID) == 0 || len(cmd.ExchangeInstanceID) > maxInstanceIDLen {
		return nil, ErrInvalidInstanceID
	}
	if err := domain.ValidateOwner(cmd.Owner); err != nil {
		return nil, err
	}
	body, err := commandBody(cmd)
	if err != nil {
		return nil, err
	}

	dst := make([]byte, 0, 128+len(cmd.ChainID)+len(cmd.ExchangeInstanceID)+len(cmd.Owner)+len(body))
	dst = appendLenPrefixed(dst, []byte(BatchCommandDomain))
	dst = appendU32(dst, cmd.ProtocolVersion)
	dst = appendLenPrefixed(dst, []byte(cmd.ChainID))
	dst = appendLenPrefixed(dst, cmd.ExchangeInstanceID)
	dst = appendLenPrefixed(dst, cmd.Owner)
	dst = appendU64(dst, cmd.Nonce)
	dst = append(dst, byte(cmd.Type))
	dst = append(dst, body...)
	return dst, nil
}

// DecodeCommandSignBytes reverses CommandSignBytes.
// PubKey and Signature are not part of the signing bytes.
// A length that does not fit in the buffer is rejected. It does not allocate it.
func DecodeCommandSignBytes(bz []byte) (Command, error) {
	r := byteReader{b: bz}
	domainBytes, err := r.prefixed()
	if err != nil {
		return Command{}, err
	}
	if string(domainBytes) != BatchCommandDomain {
		return Command{}, ErrInvalidCommand
	}
	version, err := r.u32()
	if err != nil {
		return Command{}, err
	}
	if version != BatchCommandVersion {
		return Command{}, ErrUnsupportedVersion
	}
	chain, err := r.prefixed()
	if err != nil {
		return Command{}, err
	}
	instance, err := r.prefixed()
	if err != nil {
		return Command{}, err
	}
	owner, err := r.prefixed()
	if err != nil {
		return Command{}, err
	}
	nonce, err := r.u64()
	if err != nil {
		return Command{}, err
	}
	typ, err := r.u8()
	if err != nil {
		return Command{}, err
	}
	cmd := Command{
		ProtocolVersion:    version,
		ChainID:            string(chain),
		ExchangeInstanceID: instance,
		Owner:              owner,
		Nonce:              nonce,
		Type:               CommandType(typ),
	}
	switch cmd.Type {
	case CommandTypePlace:
		place, err := r.place()
		if err != nil {
			return Command{}, err
		}
		cmd.Place = &place
	case CommandTypeCancel:
		raw, err := r.raw(32)
		if err != nil {
			return Command{}, err
		}
		var id domain.OrderID
		copy(id[:], raw)
		if id.IsZero() {
			return Command{}, domain.ErrInvalidOrderID
		}
		cmd.Cancel = &Cancel{OrderID: id}
	default:
		return Command{}, ErrInvalidCommand
	}
	if r.left() != 0 {
		return Command{}, ErrInvalidCommand
	}
	if _, err := CommandSignBytes(cmd); err != nil {
		return Command{}, err
	}
	return cmd, nil
}

type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) left() int {
	return len(r.b) - r.i
}

func (r *byteReader) u8() (byte, error) {
	if r.left() < 1 {
		return 0, ErrInvalidCommand
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *byteReader) u32() (uint32, error) {
	if r.left() < 4 {
		return 0, ErrInvalidCommand
	}
	v := binary.BigEndian.Uint32(r.b[r.i : r.i+4])
	r.i += 4
	return v, nil
}

func (r *byteReader) u64() (uint64, error) {
	if r.left() < 8 {
		return 0, ErrInvalidCommand
	}
	v := binary.BigEndian.Uint64(r.b[r.i : r.i+8])
	r.i += 8
	return v, nil
}

func (r *byteReader) raw(n int) ([]byte, error) {
	if n < 0 || r.left() < n {
		return nil, ErrInvalidCommand
	}
	out := make([]byte, n)
	copy(out, r.b[r.i:r.i+n])
	r.i += n
	return out, nil
}

func (r *byteReader) prefixed() ([]byte, error) {
	n, err := r.u64()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.left()) {
		return nil, ErrInvalidCommand
	}
	return r.raw(int(n))
}

func (r *byteReader) place() (Place, error) {
	market, err := r.u64()
	if err != nil {
		return Place{}, err
	}
	side, err := r.u8()
	if err != nil {
		return Place{}, err
	}
	typ, err := r.u8()
	if err != nil {
		return Place{}, err
	}
	tif, err := r.u8()
	if err != nil {
		return Place{}, err
	}
	qty, err := r.u64()
	if err != nil {
		return Place{}, err
	}
	price, err := r.u64()
	if err != nil {
		return Place{}, err
	}
	expiry, err := r.u64()
	if err != nil {
		return Place{}, err
	}
	client, err := r.prefixed()
	if err != nil {
		return Place{}, err
	}
	return Place{
		MarketID:      domain.MarketID(market),
		Side:          domain.Side(side),
		Type:          domain.OrderType(typ),
		TimeInForce:   domain.TimeInForce(tif),
		Quantity:      domain.Quantity(qty),
		Price:         domain.Price(price),
		ExpiryHeight:  expiry,
		ClientOrderID: client,
	}, nil
}

func commandBody(cmd Command) ([]byte, error) {
	switch cmd.Type {
	case CommandTypePlace:
		if cmd.Place == nil || cmd.Cancel != nil {
			return nil, ErrInvalidCommand
		}
		return placeBody(*cmd.Place)
	case CommandTypeCancel:
		if cmd.Cancel == nil || cmd.Place != nil {
			return nil, ErrInvalidCommand
		}
		if cmd.Cancel.OrderID.IsZero() {
			return nil, domain.ErrInvalidOrderID
		}
		return append([]byte(nil), cmd.Cancel.OrderID[:]...), nil
	default:
		return nil, ErrInvalidCommand
	}
}

func placeBody(p Place) ([]byte, error) {
	if p.MarketID == 0 {
		return nil, domain.ErrInvalidMarket
	}
	if !p.Side.Valid() {
		return nil, domain.ErrInvalidSide
	}
	if !p.Type.Valid() {
		return nil, domain.ErrInvalidOrderType
	}
	if !p.TimeInForce.Valid() {
		return nil, domain.ErrInvalidTimeInForce
	}
	if p.Quantity == 0 {
		return nil, domain.ErrInvalidQuantity
	}
	if p.Price == 0 {
		return nil, domain.ErrInvalidPrice
	}
	if len(p.ClientOrderID) > MaxClientOrderIDLength {
		return nil, ErrInvalidClientOrderID
	}
	dst := make([]byte, 0, 48+len(p.ClientOrderID))
	dst = appendU64(dst, uint64(p.MarketID))
	dst = append(dst, byte(p.Side), byte(p.Type), byte(p.TimeInForce))
	dst = appendU64(dst, uint64(p.Quantity))
	dst = appendU64(dst, uint64(p.Price))
	dst = appendU64(dst, p.ExpiryHeight)
	dst = appendLenPrefixed(dst, p.ClientOrderID)
	return dst, nil
}

// HashBatchID is SHA-256 over the batch header and the ordered commands.
// It identifies the submitted batch. It is not an exchange state root.
//
//	u64be len(domain) | domain
//	u64be batchNumber
//	u64be expectedRevision
//	u64be commandCount
//	for each command, in order:
//	  u64be len(signBytes) | signBytes
//	  u64be len(pubKey) | pubKey
//	  u64be len(signature) | signature
func HashBatchID(number, expectedRevision uint64, cmds []Command) ([32]byte, error) {
	if number == 0 {
		return [32]byte{}, ErrInvalidBatchIdentity
	}
	dst := make([]byte, 0, 64+len(BatchIDDomain))
	dst = appendLenPrefixed(dst, []byte(BatchIDDomain))
	dst = appendU64(dst, number)
	dst = appendU64(dst, expectedRevision)
	dst = appendU64(dst, uint64(len(cmds)))
	for i := range cmds {
		sign, err := CommandSignBytes(cmds[i])
		if err != nil {
			return [32]byte{}, err
		}
		if len(cmds[i].PubKey) == 0 || len(cmds[i].Signature) == 0 {
			return [32]byte{}, ErrInvalidBatchIdentity
		}
		dst = appendLenPrefixed(dst, sign)
		dst = appendLenPrefixed(dst, cmds[i].PubKey)
		dst = appendLenPrefixed(dst, cmds[i].Signature)
	}
	return sha256.Sum256(dst), nil
}

func appendU32(dst []byte, v uint32) []byte {
	return append(dst, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
