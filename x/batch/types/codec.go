package types

import (
	"encoding/binary"
	"math"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const codecVersion byte = 2

func putU64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

func putU32(dst []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
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

// EncodeParams encodes the submitter, the latest batch number, and the head commitment.
func EncodeParams(p Params) ([]byte, error) {
	if err := domain.ValidateOwner(p.Submitter); err != nil {
		return nil, err
	}
	if p.Latest == 0 && !p.Head.IsZero() {
		return nil, ErrCorrupt
	}
	dst := []byte{codecVersion, byte(len(p.Submitter))}
	dst = append(dst, p.Submitter...)
	dst = putU64(dst, p.Latest)
	dst = append(dst, p.Head[:]...)
	return dst, nil
}

// DecodeParams reverses EncodeParams.
func DecodeParams(bz []byte) (Params, error) {
	r := reader{b: bz}
	v, err := r.u8()
	if err != nil {
		return Params{}, err
	}
	if v != codecVersion {
		return Params{}, ErrCorrupt
	}
	n, err := r.u8()
	if err != nil {
		return Params{}, err
	}
	submitter, err := r.raw(int(n))
	if err != nil {
		return Params{}, err
	}
	latest, err := r.u64()
	if err != nil {
		return Params{}, err
	}
	headRaw, err := r.raw(32)
	if err != nil {
		return Params{}, err
	}
	if err := r.done(); err != nil {
		return Params{}, err
	}
	if err := domain.ValidateOwner(submitter); err != nil {
		return Params{}, ErrCorrupt
	}
	var head BatchCommitment
	copy(head[:], headRaw)
	if latest == 0 && !head.IsZero() {
		return Params{}, ErrCorrupt
	}
	return Params{Submitter: submitter, Latest: latest, Head: head}, nil
}

// EncodeUint64 encodes a batch number stored in the ID index.
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

// EncodeBatch encodes a finalized batch record.
func EncodeBatch(b Batch) ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	dst := []byte{codecVersion}
	dst = putU64(dst, b.Number)
	dst = putU64(dst, b.Height)
	dst = putU64(dst, b.PreRevision)
	dst = putU64(dst, b.PostRevision)
	dst = append(dst, b.ID[:]...)
	dst = append(dst, b.Previous[:]...)
	dst = append(dst, b.Commitment[:]...)
	dst = append(dst, b.ResultsHash[:]...)
	dst = putU32(dst, uint32(len(b.Results)))
	for _, result := range b.Results {
		encoded, err := encodeResult(result)
		if err != nil {
			return nil, err
		}
		dst = append(dst, encoded...)
	}
	return dst, nil
}

func encodeResult(r CommandResult) ([]byte, error) {
	if len(r.Trades) > math.MaxUint32 {
		return nil, ErrCorrupt
	}
	dst := putU32(nil, r.Index)
	dst = append(dst, r.Type, byte(len(r.Owner)))
	dst = append(dst, r.Owner...)
	dst = append(dst, r.OrderID[:]...)
	dst = append(dst, r.Status)
	dst = putU64(dst, r.Remaining)
	dst = putU32(dst, uint32(len(r.Trades)))
	for _, trade := range r.Trades {
		dst = putU64(dst, uint64(trade.MarketID))
		dst = putU64(dst, trade.Sequence)
	}
	return dst, nil
}

// DecodeBatch reverses EncodeBatch.
func DecodeBatch(bz []byte) (Batch, error) {
	r := reader{b: bz}
	v, err := r.u8()
	if err != nil {
		return Batch{}, err
	}
	if v != codecVersion {
		return Batch{}, ErrCorrupt
	}
	number, err := r.u64()
	if err != nil {
		return Batch{}, err
	}
	height, err := r.u64()
	if err != nil {
		return Batch{}, err
	}
	pre, err := r.u64()
	if err != nil {
		return Batch{}, err
	}
	post, err := r.u64()
	if err != nil {
		return Batch{}, err
	}
	idRaw, err := r.raw(32)
	if err != nil {
		return Batch{}, err
	}
	prevRaw, err := r.raw(32)
	if err != nil {
		return Batch{}, err
	}
	commitmentRaw, err := r.raw(32)
	if err != nil {
		return Batch{}, err
	}
	hashRaw, err := r.raw(32)
	if err != nil {
		return Batch{}, err
	}
	count, err := r.u32()
	if err != nil {
		return Batch{}, err
	}
	if count == 0 || int(count) > MaxCommands {
		return Batch{}, ErrCorrupt
	}
	results := make([]CommandResult, 0, count)
	for i := uint32(0); i < count; i++ {
		result, err := decodeResult(&r)
		if err != nil {
			return Batch{}, err
		}
		results = append(results, result)
	}
	if err := r.done(); err != nil {
		return Batch{}, err
	}
	var id BatchID
	copy(id[:], idRaw)
	var previous BatchCommitment
	copy(previous[:], prevRaw)
	var commitment BatchCommitment
	copy(commitment[:], commitmentRaw)
	var resultsHash ResultsHash
	copy(resultsHash[:], hashRaw)
	batch := Batch{
		Number:       number,
		ID:           id,
		Height:       height,
		PreRevision:  pre,
		PostRevision: post,
		Previous:     previous,
		Commitment:   commitment,
		ResultsHash:  resultsHash,
		Results:      results,
	}
	if err := batch.Validate(); err != nil {
		return Batch{}, err
	}
	return batch, nil
}

func decodeResult(r *reader) (CommandResult, error) {
	index, err := r.u32()
	if err != nil {
		return CommandResult{}, err
	}
	typ, err := r.u8()
	if err != nil {
		return CommandResult{}, err
	}
	n, err := r.u8()
	if err != nil {
		return CommandResult{}, err
	}
	owner, err := r.raw(int(n))
	if err != nil {
		return CommandResult{}, err
	}
	idRaw, err := r.raw(32)
	if err != nil {
		return CommandResult{}, err
	}
	status, err := r.u8()
	if err != nil {
		return CommandResult{}, err
	}
	remaining, err := r.u64()
	if err != nil {
		return CommandResult{}, err
	}
	tradesN, err := r.u32()
	if err != nil {
		return CommandResult{}, err
	}
	trades := make([]TradeRef, 0, tradesN)
	for i := uint32(0); i < tradesN; i++ {
		market, err := r.u64()
		if err != nil {
			return CommandResult{}, err
		}
		seq, err := r.u64()
		if err != nil {
			return CommandResult{}, err
		}
		trades = append(trades, TradeRef{MarketID: domain.MarketID(market), Sequence: seq})
	}
	var id domain.OrderID
	copy(id[:], idRaw)
	return CommandResult{
		Index:     index,
		Type:      typ,
		Owner:     owner,
		OrderID:   id,
		Status:    status,
		Remaining: remaining,
		Trades:    trades,
	}, nil
}
