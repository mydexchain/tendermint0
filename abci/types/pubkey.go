package types

import (
	"github.com/mydexchain/tendermint0/crypto/ed25519"
	cryptoenc "github.com/mydexchain/tendermint0/crypto/encoding"
)

const (
	PubKeyEd25519 = "ed25519"
)

func Ed25519ValidatorUpdate(pk []byte, power int64) ValidatorUpdate {
	pke := ed25519.PubKey(pk)
	pkp, err := cryptoenc.PubKeyToProto(pke)
	if err != nil {
		panic(err)
	}

	return ValidatorUpdate{
		// Address:
		PubKey: pkp,
		Power:  power,
	}
}
