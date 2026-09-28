package sequencer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const signedCodecVersion byte = 1

// encodeSigned is the journal form of one signed command:
// version, length-prefixed sign bytes, public key, and signature.
func encodeSigned(cmd canonical.Command) ([]byte, error) {
	sign, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		return nil, err
	}
	if len(cmd.PubKey) == 0 || len(cmd.Signature) == 0 {
		return nil, ErrMalformed
	}
	var buf bytes.Buffer
	buf.WriteByte(signedCodecVersion)
	if err := writeLP(&buf, sign); err != nil {
		return nil, err
	}
	if err := writeLP(&buf, cmd.PubKey); err != nil {
		return nil, err
	}
	if err := writeLP(&buf, cmd.Signature); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeSigned(bz []byte) (canonical.Command, error) {
	if len(bz) == 0 || bz[0] != signedCodecVersion {
		return canonical.Command{}, fmt.Errorf("%w: signed command version", ErrJournalCorrupt)
	}
	r := &reader{b: bz[1:]}
	sign, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	pub, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	sig, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	if err := r.done(); err != nil {
		return canonical.Command{}, err
	}
	cmd, err := parseSignBytes(sign)
	if err != nil {
		return canonical.Command{}, err
	}
	cmd.PubKey = pub
	cmd.Signature = sig
	again, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		return canonical.Command{}, fmt.Errorf("%w: %v", ErrJournalCorrupt, err)
	}
	if !bytes.Equal(again, sign) {
		return canonical.Command{}, fmt.Errorf("%w: sign bytes do not match command", ErrJournalCorrupt)
	}
	return cmd, nil
}

func commandID(encoded []byte) [32]byte {
	return sha256.Sum256(encoded)
}

func parseSignBytes(bz []byte) (canonical.Command, error) {
	r := &reader{b: bz}
	domainName, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	if string(domainName) != canonical.BatchCommandDomain {
		return canonical.Command{}, fmt.Errorf("%w: command domain", ErrJournalCorrupt)
	}
	version, err := r.u32()
	if err != nil {
		return canonical.Command{}, err
	}
	chainID, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	instance, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	owner, err := r.lp()
	if err != nil {
		return canonical.Command{}, err
	}
	nonce, err := r.u64()
	if err != nil {
		return canonical.Command{}, err
	}
	typ, err := r.u8()
	if err != nil {
		return canonical.Command{}, err
	}
	cmd := canonical.Command{
		ProtocolVersion:    version,
		ChainID:            string(chainID),
		ExchangeInstanceID: instance,
		Owner:              owner,
		Nonce:              nonce,
		Type:               canonical.CommandType(typ),
	}
	switch cmd.Type {
	case canonical.CommandTypePlace:
		place, err := parsePlace(r)
		if err != nil {
			return canonical.Command{}, err
		}
		cmd.Place = &place
	case canonical.CommandTypeCancel:
		raw, err := r.bytes(len(domain.OrderID{}))
		if err != nil {
			return canonical.Command{}, err
		}
		var id domain.OrderID
		copy(id[:], raw)
		cmd.Cancel = &canonical.Cancel{OrderID: id}
	default:
		return canonical.Command{}, fmt.Errorf("%w: command type", ErrJournalCorrupt)
	}
	if err := r.done(); err != nil {
		return canonical.Command{}, err
	}
	return cmd, nil
}

func parsePlace(r *reader) (canonical.Place, error) {
	market, err := r.u64()
	if err != nil {
		return canonical.Place{}, err
	}
	side, err := r.u8()
	if err != nil {
		return canonical.Place{}, err
	}
	orderType, err := r.u8()
	if err != nil {
		return canonical.Place{}, err
	}
	tif, err := r.u8()
	if err != nil {
		return canonical.Place{}, err
	}
	qty, err := r.u64()
	if err != nil {
		return canonical.Place{}, err
	}
	price, err := r.u64()
	if err != nil {
		return canonical.Place{}, err
	}
	expiry, err := r.u64()
	if err != nil {
		return canonical.Place{}, err
	}
	clientID, err := r.lp()
	if err != nil {
		return canonical.Place{}, err
	}
	return canonical.Place{
		MarketID:      domain.MarketID(market),
		Side:          domain.Side(side),
		Type:          domain.OrderType(orderType),
		TimeInForce:   domain.TimeInForce(tif),
		Quantity:      domain.Quantity(qty),
		Price:         domain.Price(price),
		ExpiryHeight:  expiry,
		ClientOrderID: clientID,
	}, nil
}

func writeLP(buf *bytes.Buffer, b []byte) error {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	if _, err := buf.Write(n[:]); err != nil {
		return err
	}
	_, err := buf.Write(b)
	return err
}

const maxBlob = 1 << 20

type reader struct {
	b []byte
	i int
}

func (r *reader) u8() (byte, error) {
	if r.i >= len(r.b) {
		return 0, fmt.Errorf("%w: short buffer", ErrJournalCorrupt)
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	raw, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(raw), nil
}

func (r *reader) u64() (uint64, error) {
	raw, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw), nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.i+n > len(r.b) {
		return nil, fmt.Errorf("%w: short buffer", ErrJournalCorrupt)
	}
	out := append([]byte(nil), r.b[r.i:r.i+n]...)
	r.i += n
	return out, nil
}

func (r *reader) lp() ([]byte, error) {
	n, err := r.u64()
	if err != nil {
		return nil, err
	}
	if n > maxBlob {
		return nil, fmt.Errorf("%w: length prefix", ErrJournalCorrupt)
	}
	if n == 0 {
		return []byte{}, nil
	}
	return r.bytes(int(n))
}

func (r *reader) done() error {
	if r.i != len(r.b) {
		return fmt.Errorf("%w: trailing bytes", ErrJournalCorrupt)
	}
	return nil
}

func cloneCommand(cmd canonical.Command) canonical.Command {
	out := cmd
	out.ExchangeInstanceID = append([]byte(nil), cmd.ExchangeInstanceID...)
	out.Owner = append([]byte(nil), cmd.Owner...)
	out.PubKey = append([]byte(nil), cmd.PubKey...)
	out.Signature = append([]byte(nil), cmd.Signature...)
	if cmd.Place != nil {
		p := *cmd.Place
		p.ClientOrderID = append([]byte(nil), cmd.Place.ClientOrderID...)
		out.Place = &p
	}
	if cmd.Cancel != nil {
		c := *cmd.Cancel
		out.Cancel = &c
	}
	return out
}
