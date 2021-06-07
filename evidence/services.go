package evidence

import (
	"github.com/mydexchain/tendermint0/types"
)

//go:generate mockery --case underscore --name BlockStore

type BlockStore interface {
	LoadBlockMeta(height int64) *types.BlockMeta
}
