package rpc

import (
	"github.com/mydexchain/tendermint0/crypto/merkle"
)

func defaultProofRuntime() *merkle.ProofRuntime {
	prt := merkle.NewProofRuntime()
	prt.RegisterOpDecoder(
		merkle.ProofOpValue,
		merkle.ValueOpDecoder,
	)
	return prt
}
