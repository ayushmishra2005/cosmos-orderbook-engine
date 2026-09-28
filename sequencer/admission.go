package sequencer

import (
	"bytes"
	"errors"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

// validate checks one command before it is assigned a sequencer position.
// It does not match, reserve, settle, or decide a final nonce.
func validate(cfg Config, cmd canonical.Command) error {
	switch cmd.Type {
	case canonical.CommandTypePlace, canonical.CommandTypeCancel:
	default:
		return ErrUnsupportedCommand
	}
	if cmd.ProtocolVersion != canonical.BatchCommandVersion {
		return ErrUnsupportedVersion
	}
	if cmd.ChainID != cfg.ChainID {
		return ErrChainID
	}
	if !bytes.Equal(cmd.ExchangeInstanceID, cfg.InstanceID) {
		return ErrInstance
	}
	if err := domain.ValidateOwner(cmd.Owner); err != nil {
		return ErrOwner
	}
	if cmd.Nonce == 0 {
		return ErrNonce
	}
	if len(cmd.PubKey) != secp256k1.PubKeySize || (cmd.PubKey[0] != 0x02 && cmd.PubKey[0] != 0x03) {
		return ErrPubKey
	}
	if len(cmd.Signature) != 64 {
		return ErrSignature
	}
	if _, err := canonical.CommandSignBytes(cmd); err != nil {
		return admissionError(err)
	}
	if err := structure(cmd); err != nil {
		return err
	}
	encoded, err := encodeSigned(cmd)
	if err != nil {
		return admissionError(err)
	}
	if len(encoded) > cfg.MaxCommandBytes {
		return ErrTooLarge
	}
	sign, err := canonical.CommandSignBytes(cmd)
	if err != nil {
		return admissionError(err)
	}
	pub := &secp256k1.PubKey{Key: append([]byte(nil), cmd.PubKey...)}
	if !pub.VerifySignature(sign, cmd.Signature) {
		return ErrSignature
	}
	if !bytes.Equal([]byte(sdk.AccAddress(pub.Address())), cmd.Owner) {
		return ErrPubKeyMismatch
	}
	return nil
}

func structure(cmd canonical.Command) error {
	if cmd.Type != canonical.CommandTypePlace {
		return nil
	}
	p := cmd.Place
	if p == nil {
		return ErrMalformed
	}
	if p.Type == domain.OrderTypeMarket && p.TimeInForce != domain.TimeInForceIOC && p.TimeInForce != domain.TimeInForceFOK {
		return ErrMalformed
	}
	if p.TimeInForce == domain.TimeInForceGTD {
		if p.ExpiryHeight == 0 {
			return ErrMalformed
		}
		return nil
	}
	if p.ExpiryHeight != 0 {
		return ErrMalformed
	}
	return nil
}

func admissionError(err error) error {
	switch {
	case errors.Is(err, canonical.ErrUnsupportedVersion):
		return ErrUnsupportedVersion
	case errors.Is(err, canonical.ErrInvalidChainID):
		return ErrChainID
	case errors.Is(err, canonical.ErrInvalidInstanceID):
		return ErrInstance
	case errors.Is(err, domain.ErrInvalidOwner):
		return ErrOwner
	case errors.Is(err, canonical.ErrInvalidCommand):
		return ErrMalformed
	default:
		return ErrMalformed
	}
}
