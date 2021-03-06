package proxy

import (
	abci "github.com/mydexchain/tendermint0/abci/types"
	"github.com/mydexchain/tendermint0/version"
)

// RequestInfo contains all the information for sending
// the abci.RequestInfo message during handshake with the app.
// It contains only compile-time version information.
var RequestInfo = abci.RequestInfo{
	Version:      version.Version,
	BlockVersion: version.BlockProtocol,
	P2PVersion:   version.P2PProtocol,
}
