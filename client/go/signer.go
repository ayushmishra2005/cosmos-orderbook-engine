package orderbook

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
)

type keyringSigner struct {
	kr   keyring.Keyring
	name string
	addr sdk.AccAddress
	pub  cryptotypes.PubKey
}

// NewKeyringSigner loads a named key from a Cosmos SDK keyring.
// The private key stays in the keyring.
func NewKeyringSigner(kr keyring.Keyring, name string) (Signer, error) {
	if kr == nil || name == "" {
		return nil, fmt.Errorf("%w: keyring signer", ErrInvalidArgument)
	}
	rec, err := kr.Key(name)
	if err != nil {
		return nil, fmt.Errorf("orderbook: key %s: %w", name, err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("orderbook: key %s: %w", name, err)
	}
	pub, err := rec.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("orderbook: key %s: %w", name, err)
	}
	return keyringSigner{kr: kr, name: name, addr: addr, pub: pub}, nil
}

func (s keyringSigner) Address() sdk.AccAddress { return s.addr }

func (s keyringSigner) PubKey() cryptotypes.PubKey { return s.pub }

func (s keyringSigner) Sign(bz []byte) ([]byte, error) {
	sig, _, err := s.kr.Sign(s.name, bz, signing.SignMode_SIGN_MODE_DIRECT)
	if err != nil {
		return nil, fmt.Errorf("orderbook: sign: %w", err)
	}
	return sig, nil
}
