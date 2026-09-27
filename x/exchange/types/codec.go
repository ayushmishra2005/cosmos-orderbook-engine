package types

import (
	"encoding/binary"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func putU64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

type reader struct {
	b []byte
	i int
}

func (r *reader) u8() (byte, error) {
	if r.i >= len(r.b) {
		return 0, ErrCorrupt
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if len(r.b)-r.i < 4 {
		return 0, ErrCorrupt
	}
	v := binary.BigEndian.Uint32(r.b[r.i : r.i+4])
	r.i += 4
	return v, nil
}

func (r *reader) u64() (uint64, error) {
	if len(r.b)-r.i < 8 {
		return 0, ErrCorrupt
	}
	v := binary.BigEndian.Uint64(r.b[r.i : r.i+8])
	r.i += 8
	return v, nil
}

func (r *reader) raw(n int) ([]byte, error) {
	if n < 0 || len(r.b)-r.i < n {
		return nil, ErrCorrupt
	}
	out := make([]byte, n)
	copy(out, r.b[r.i:r.i+n])
	r.i += n
	return out, nil
}

func (r *reader) done() error {
	if r.i != len(r.b) {
		return ErrCorrupt
	}
	return nil
}

func (r *reader) version() error {
	v, err := r.u8()
	if err != nil {
		return err
	}
	if v != codecVersion {
		return ErrCorrupt
	}
	return nil
}

func (r *reader) owner() ([]byte, error) {
	n, err := r.u8()
	if err != nil {
		return nil, err
	}
	owner, err := r.raw(int(n))
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateOwner(owner); err != nil {
		return nil, ErrCorrupt
	}
	return owner, nil
}

// EncodeMarket encodes a market record. The market ID is also the key.
func EncodeMarket(m Market) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	dst := []byte{codecVersion}
	dst = putU64(dst, uint64(m.ID))
	dst = putU64(dst, uint64(m.BaseAssetID))
	dst = putU64(dst, uint64(m.QuoteAssetID))
	dst = putU64(dst, m.BaseLotSize)
	dst = putU64(dst, m.QuoteAtomsPerTickPerLot)
	dst = putU64(dst, m.MakerFeePPM)
	dst = putU64(dst, m.TakerFeePPM)
	var visits [4]byte
	binary.BigEndian.PutUint32(visits[:], m.MaxMakerVisits)
	dst = append(dst, visits[:]...)
	if m.Enabled {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	return dst, nil
}

// DecodeMarket reverses EncodeMarket.
func DecodeMarket(bz []byte) (Market, error) {
	r := reader{b: bz}
	if err := r.version(); err != nil {
		return Market{}, err
	}
	var m Market
	id, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	base, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	quote, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	lot, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	atoms, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	maker, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	taker, err := r.u64()
	if err != nil {
		return Market{}, err
	}
	visits, err := r.u32()
	if err != nil {
		return Market{}, err
	}
	enabled, err := r.u8()
	if err != nil {
		return Market{}, err
	}
	if err := r.done(); err != nil {
		return Market{}, err
	}
	if enabled > 1 {
		return Market{}, ErrCorrupt
	}
	m = Market{
		ID:                      domain.MarketID(id),
		BaseAssetID:             domain.AssetID(base),
		QuoteAssetID:            domain.AssetID(quote),
		BaseLotSize:             lot,
		QuoteAtomsPerTickPerLot: atoms,
		MakerFeePPM:             maker,
		TakerFeePPM:             taker,
		MaxMakerVisits:          visits,
		Enabled:                 enabled == 1,
	}
	if err := m.Validate(); err != nil {
		return Market{}, ErrCorrupt
	}
	return m, nil
}

// EncodeBalance encodes available and locked atoms.
func EncodeBalance(b Balance) []byte {
	dst := []byte{codecVersion}
	dst = putU64(dst, b.Available)
	dst = putU64(dst, b.Locked)
	return dst
}

// DecodeBalance reverses EncodeBalance.
func DecodeBalance(bz []byte) (Balance, error) {
	r := reader{b: bz}
	if err := r.version(); err != nil {
		return Balance{}, err
	}
	available, err := r.u64()
	if err != nil {
		return Balance{}, err
	}
	locked, err := r.u64()
	if err != nil {
		return Balance{}, err
	}
	if err := r.done(); err != nil {
		return Balance{}, err
	}
	return Balance{Available: available, Locked: locked}, nil
}

// EncodeOrder encodes the active-order record. The order ID is the key.
func EncodeOrder(o StoredOrder) ([]byte, error) {
	if err := o.Order.ValidateResting(); err != nil {
		return nil, err
	}
	dst := []byte{codecVersion}
	dst = append(dst, byte(len(o.Order.Owner)))
	dst = append(dst, o.Order.Owner...)
	dst = putU64(dst, uint64(o.Order.MarketID))
	dst = append(dst, byte(o.Order.Side), byte(o.Order.Type), byte(o.Order.TimeInForce))
	dst = putU64(dst, uint64(o.Order.Price))
	dst = putU64(dst, uint64(o.Order.OriginalQuantity))
	dst = putU64(dst, uint64(o.Order.RemainingQuantity))
	dst = putU64(dst, uint64(o.Order.Sequence))
	dst = putU64(dst, o.Order.ExpiryHeight)
	dst = putU64(dst, o.Order.CommandNonce)
	dst = putU64(dst, o.TakerGross)
	dst = putU64(dst, o.MakerGross)
	return dst, nil
}

// DecodeOrder reverses EncodeOrder. id is the key, not a field in bz.
func DecodeOrder(id domain.OrderID, bz []byte) (StoredOrder, error) {
	r := reader{b: bz}
	if err := r.version(); err != nil {
		return StoredOrder{}, err
	}
	owner, err := r.owner()
	if err != nil {
		return StoredOrder{}, err
	}
	marketID, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	side, err := r.u8()
	if err != nil {
		return StoredOrder{}, err
	}
	typ, err := r.u8()
	if err != nil {
		return StoredOrder{}, err
	}
	tif, err := r.u8()
	if err != nil {
		return StoredOrder{}, err
	}
	price, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	original, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	remaining, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	sequence, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	expiry, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	nonce, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	takerGross, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	makerGross, err := r.u64()
	if err != nil {
		return StoredOrder{}, err
	}
	if err := r.done(); err != nil {
		return StoredOrder{}, err
	}
	o := StoredOrder{
		Order: domain.Order{
			ID:                id,
			Owner:             owner,
			MarketID:          domain.MarketID(marketID),
			Side:              domain.Side(side),
			Type:              domain.OrderType(typ),
			TimeInForce:       domain.TimeInForce(tif),
			Price:             domain.Price(price),
			OriginalQuantity:  domain.Quantity(original),
			RemainingQuantity: domain.Quantity(remaining),
			Sequence:          domain.Sequence(sequence),
			ExpiryHeight:      expiry,
			CommandNonce:      nonce,
		},
		TakerGross: takerGross,
		MakerGross: makerGross,
	}
	if err := o.Order.ValidateResting(); err != nil {
		return StoredOrder{}, ErrCorrupt
	}
	return o, nil
}

// EncodeTrade encodes a trade record. The market and sequence are also the key.
func EncodeTrade(t Trade) ([]byte, error) {
	if t.MarketID == 0 || t.Sequence == 0 || t.Price == 0 || t.Quantity == 0 {
		return nil, ErrCorrupt
	}
	if t.MakerOrderID.IsZero() || t.TakerOrderID.IsZero() {
		return nil, ErrCorrupt
	}
	if err := domain.ValidateOwner(t.Buyer); err != nil {
		return nil, err
	}
	if err := domain.ValidateOwner(t.Seller); err != nil {
		return nil, err
	}
	dst := []byte{codecVersion}
	dst = putU64(dst, uint64(t.MarketID))
	dst = putU64(dst, t.Sequence)
	dst = append(dst, t.MakerOrderID[:]...)
	dst = append(dst, t.TakerOrderID[:]...)
	dst = putU64(dst, uint64(t.Price))
	dst = putU64(dst, uint64(t.Quantity))
	dst = putU64(dst, t.BaseAmount)
	dst = putU64(dst, t.QuoteAmount)
	dst = putU64(dst, t.MakerFee)
	dst = putU64(dst, t.TakerFee)
	dst = append(dst, byte(len(t.Buyer)))
	dst = append(dst, t.Buyer...)
	dst = append(dst, byte(len(t.Seller)))
	dst = append(dst, t.Seller...)
	return dst, nil
}

// DecodeTrade reverses EncodeTrade.
func DecodeTrade(bz []byte) (Trade, error) {
	r := reader{b: bz}
	if err := r.version(); err != nil {
		return Trade{}, err
	}
	marketID, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	sequence, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	makerRaw, err := r.raw(32)
	if err != nil {
		return Trade{}, err
	}
	takerRaw, err := r.raw(32)
	if err != nil {
		return Trade{}, err
	}
	price, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	qty, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	base, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	quote, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	makerFee, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	takerFee, err := r.u64()
	if err != nil {
		return Trade{}, err
	}
	buyer, err := r.owner()
	if err != nil {
		return Trade{}, err
	}
	seller, err := r.owner()
	if err != nil {
		return Trade{}, err
	}
	if err := r.done(); err != nil {
		return Trade{}, err
	}
	var makerID, takerID domain.OrderID
	copy(makerID[:], makerRaw)
	copy(takerID[:], takerRaw)
	t := Trade{
		MarketID:     domain.MarketID(marketID),
		Sequence:     sequence,
		MakerOrderID: makerID,
		TakerOrderID: takerID,
		Price:        domain.Price(price),
		Quantity:     domain.Quantity(qty),
		BaseAmount:   base,
		QuoteAmount:  quote,
		MakerFee:     makerFee,
		TakerFee:     takerFee,
		Buyer:        buyer,
		Seller:       seller,
	}
	if t.MarketID == 0 || t.Sequence == 0 || t.Price == 0 || t.Quantity == 0 || makerID.IsZero() || takerID.IsZero() {
		return Trade{}, ErrCorrupt
	}
	return t, nil
}

// EncodeUint64 encodes a counter value. Missing keys mean zero and are not
// encoded as this value by the keeper.
func EncodeUint64(v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return buf[:]
}

// DecodeUint64 reverses EncodeUint64.
func DecodeUint64(bz []byte) (uint64, error) {
	if len(bz) != 8 {
		return 0, ErrCorrupt
	}
	return binary.BigEndian.Uint64(bz), nil
}
