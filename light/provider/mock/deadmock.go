package mock

import (
	"errors"

	"github.com/mydexchain/tendermint0/light/provider"
	"github.com/mydexchain/tendermint0/types"
)

var errNoResp = errors.New("no response from provider")

type deadMock struct {
	chainID string
}

// NewDeadMock creates a mock provider that always errors.
func NewDeadMock(chainID string) provider.Provider {
	return &deadMock{chainID: chainID}
}

func (p *deadMock) ChainID() string { return p.chainID }

func (p *deadMock) String() string { return "deadMock" }

func (p *deadMock) SignedHeader(height int64) (*types.SignedHeader, error) {
	return nil, errNoResp
}

func (p *deadMock) ValidatorSet(height int64) (*types.ValidatorSet, error) {
	return nil, errNoResp
}
func (p *deadMock) ReportEvidence(ev types.Evidence) error {
	return errNoResp
}
