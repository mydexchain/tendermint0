package state_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mydexchain/tendermint0/crypto"
	"github.com/mydexchain/tendermint0/libs/bytes"
	tmrand "github.com/mydexchain/tendermint0/libs/rand"
	"github.com/mydexchain/tendermint0/proto/tendermint/version"

	"github.com/mydexchain/tendermint0/crypto/ed25519"
	"github.com/mydexchain/tendermint0/crypto/tmhash"
	"github.com/mydexchain/tendermint0/libs/log"
	memmock "github.com/mydexchain/tendermint0/mempool/mock"
	tmproto "github.com/mydexchain/tendermint0/proto/tendermint/types"
	sm "github.com/mydexchain/tendermint0/state"
	"github.com/mydexchain/tendermint0/state/mocks"
	"github.com/mydexchain/tendermint0/types"
	tmtime "github.com/mydexchain/tendermint0/types/time"
)

const validationTestsStopHeight int64 = 10

var defaultTestTime = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

func TestValidateBlockHeader(t *testing.T) {
	proxyApp := newTestApp()
	require.NoError(t, proxyApp.Start())
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, privVals := makeState(3, 1)
	blockExec := sm.NewBlockExecutor(
		stateDB,
		log.TestingLogger(),
		proxyApp.Consensus(),
		memmock.Mempool{},
		sm.MockEvidencePool{},
	)
	lastCommit := types.NewCommit(0, 0, types.BlockID{}, nil)

	// some bad values
	wrongHash := tmhash.Sum([]byte("this hash is wrong"))
	wrongVersion1 := state.Version.Consensus
	wrongVersion1.Block += 2
	wrongVersion2 := state.Version.Consensus
	wrongVersion2.App += 2

	// Manipulation of any header field causes failure.
	testCases := []struct {
		name          string
		malleateBlock func(block *types.Block)
	}{
		{"Version wrong1", func(block *types.Block) { block.Version = wrongVersion1 }},
		{"Version wrong2", func(block *types.Block) { block.Version = wrongVersion2 }},
		{"ChainID wrong", func(block *types.Block) { block.ChainID = "not-the-real-one" }},
		{"Height wrong", func(block *types.Block) { block.Height += 10 }},
		{"Time wrong", func(block *types.Block) { block.Time = block.Time.Add(-time.Second * 1) }},

		{"LastBlockID wrong", func(block *types.Block) { block.LastBlockID.PartSetHeader.Total += 10 }},
		{"LastCommitHash wrong", func(block *types.Block) { block.LastCommitHash = wrongHash }},
		{"DataHash wrong", func(block *types.Block) { block.DataHash = wrongHash }},

		{"ValidatorsHash wrong", func(block *types.Block) { block.ValidatorsHash = wrongHash }},
		{"NextValidatorsHash wrong", func(block *types.Block) { block.NextValidatorsHash = wrongHash }},
		{"ConsensusHash wrong", func(block *types.Block) { block.ConsensusHash = wrongHash }},
		{"AppHash wrong", func(block *types.Block) { block.AppHash = wrongHash }},
		{"LastResultsHash wrong", func(block *types.Block) { block.LastResultsHash = wrongHash }},

		{"EvidenceHash wrong", func(block *types.Block) { block.EvidenceHash = wrongHash }},
		{"Proposer wrong", func(block *types.Block) { block.ProposerAddress = ed25519.GenPrivKey().PubKey().Address() }},
		{"Proposer invalid", func(block *types.Block) { block.ProposerAddress = []byte("wrong size") }},
	}

	// Build up state for multiple heights
	for height := int64(1); height < validationTestsStopHeight; height++ {
		proposerAddr := state.Validators.GetProposer().Address
		/*
			Invalid blocks don't pass
		*/
		for _, tc := range testCases {
			block, _ := state.MakeBlock(height, makeTxs(height), lastCommit, nil, proposerAddr)
			tc.malleateBlock(block)
			err := blockExec.ValidateBlock(state, block)
			require.Error(t, err, tc.name)
		}

		/*
			A good block passes
		*/
		var err error
		state, _, lastCommit, err = makeAndCommitGoodBlock(state, height, lastCommit, proposerAddr, blockExec, privVals, nil)
		require.NoError(t, err, "height %d", height)
	}
}

func TestValidateBlockCommit(t *testing.T) {
	proxyApp := newTestApp()
	require.NoError(t, proxyApp.Start())
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, privVals := makeState(1, 1)
	blockExec := sm.NewBlockExecutor(
		stateDB,
		log.TestingLogger(),
		proxyApp.Consensus(),
		memmock.Mempool{},
		sm.MockEvidencePool{},
	)
	lastCommit := types.NewCommit(0, 0, types.BlockID{}, nil)
	wrongSigsCommit := types.NewCommit(1, 0, types.BlockID{}, nil)
	badPrivVal := types.NewMockPV()

	for height := int64(1); height < validationTestsStopHeight; height++ {
		proposerAddr := state.Validators.GetProposer().Address
		if height > 1 {
			/*
				#2589: ensure state.LastValidators.VerifyCommit fails here
			*/
			// should be height-1 instead of height
			wrongHeightVote, err := types.MakeVote(
				height,
				state.LastBlockID,
				state.Validators,
				privVals[proposerAddr.String()],
				chainID,
				time.Now(),
			)
			require.NoError(t, err, "height %d", height)
			wrongHeightCommit := types.NewCommit(
				wrongHeightVote.Height,
				wrongHeightVote.Round,
				state.LastBlockID,
				[]types.CommitSig{wrongHeightVote.CommitSig()},
			)
			block, _ := state.MakeBlock(height, makeTxs(height), wrongHeightCommit, nil, proposerAddr)
			err = blockExec.ValidateBlock(state, block)
			_, isErrInvalidCommitHeight := err.(types.ErrInvalidCommitHeight)
			require.True(t, isErrInvalidCommitHeight, "expected ErrInvalidCommitHeight at height %d but got: %v", height, err)

			/*
				#2589: test len(block.LastCommit.Signatures) == state.LastValidators.Size()
			*/
			block, _ = state.MakeBlock(height, makeTxs(height), wrongSigsCommit, nil, proposerAddr)
			err = blockExec.ValidateBlock(state, block)
			_, isErrInvalidCommitSignatures := err.(types.ErrInvalidCommitSignatures)
			require.True(t, isErrInvalidCommitSignatures,
				"expected ErrInvalidCommitSignatures at height %d, but got: %v",
				height,
				err,
			)
		}

		/*
			A good block passes
		*/
		var err error
		var blockID types.BlockID
		state, blockID, lastCommit, err = makeAndCommitGoodBlock(
			state,
			height,
			lastCommit,
			proposerAddr,
			blockExec,
			privVals,
			nil,
		)
		require.NoError(t, err, "height %d", height)

		/*
			wrongSigsCommit is fine except for the extra bad precommit
		*/
		goodVote, err := types.MakeVote(height,
			blockID,
			state.Validators,
			privVals[proposerAddr.String()],
			chainID,
			time.Now(),
		)
		require.NoError(t, err, "height %d", height)

		bpvPubKey, err := badPrivVal.GetPubKey()
		require.NoError(t, err)

		badVote := &types.Vote{
			ValidatorAddress: bpvPubKey.Address(),
			ValidatorIndex:   0,
			Height:           height,
			Round:            0,
			Timestamp:        tmtime.Now(),
			Type:             tmproto.PrecommitType,
			BlockID:          blockID,
		}

		g := goodVote.ToProto()
		b := badVote.ToProto()

		err = badPrivVal.SignVote(chainID, g)
		require.NoError(t, err, "height %d", height)
		err = badPrivVal.SignVote(chainID, b)
		require.NoError(t, err, "height %d", height)

		goodVote.Signature, badVote.Signature = g.Signature, b.Signature

		wrongSigsCommit = types.NewCommit(goodVote.Height, goodVote.Round,
			blockID, []types.CommitSig{goodVote.CommitSig(), badVote.CommitSig()})
	}
}

func TestValidateBlockEvidence(t *testing.T) {
	proxyApp := newTestApp()
	require.NoError(t, proxyApp.Start())
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, privVals := makeState(4, 1)
	defaultEvidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

	evpool := &mocks.EvidencePool{}
	evpool.On("IsPending", mock.AnythingOfType("*types.DuplicateVoteEvidence")).Return(false)
	evpool.On("IsCommitted", mock.AnythingOfType("*types.DuplicateVoteEvidence")).Return(false)
	evpool.On("Header", mock.AnythingOfType("int64")).Return(func(height int64) *types.Header {
		return &types.Header{
			Time:   defaultEvidenceTime,
			Height: height,
		}
	})
	evpool.On("Update", mock.AnythingOfType("*types.Block"), mock.AnythingOfType("state.State")).Return()

	state.ConsensusParams.Evidence.MaxNum = 3
	blockExec := sm.NewBlockExecutor(
		stateDB,
		log.TestingLogger(),
		proxyApp.Consensus(),
		memmock.Mempool{},
		evpool,
	)
	lastCommit := types.NewCommit(0, 0, types.BlockID{}, nil)

	for height := int64(1); height < validationTestsStopHeight; height++ {
		proposerAddr := state.Validators.GetProposer().Address
		maxNumEvidence := state.ConsensusParams.Evidence.MaxNum
		if height > 1 {
			/*
				A block with too much evidence fails
			*/
			require.True(t, maxNumEvidence > 2)
			evidence := make([]types.Evidence, 0)
			// one more than the maximum allowed evidence
			for i := uint32(0); i <= maxNumEvidence; i++ {
				evidence = append(evidence, types.NewMockDuplicateVoteEvidenceWithValidator(height, time.Now(),
					privVals[proposerAddr.String()], chainID))
			}
			block, _ := state.MakeBlock(height, makeTxs(height), lastCommit, evidence, proposerAddr)
			err := blockExec.ValidateBlock(state, block)
			if assert.Error(t, err) {
				_, ok := err.(*types.ErrEvidenceOverflow)
				require.True(t, ok, "expected error to be of type ErrEvidenceOverflow at height %d but got %v", height, err)
			}
		}

		/*
			A good block with several pieces of good evidence passes
		*/
		require.True(t, maxNumEvidence > 2)
		evidence := make([]types.Evidence, 0)
		// precisely the amount of allowed evidence
		for i := int32(0); uint32(i) < maxNumEvidence; i++ {
			// make different evidence for each validator
			_, val := state.Validators.GetByIndex(i)
			evidence = append(evidence, types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultEvidenceTime,
				privVals[val.Address.String()], chainID))
		}

		var err error
		state, _, lastCommit, err = makeAndCommitGoodBlock(
			state,
			height,
			lastCommit,
			proposerAddr,
			blockExec,
			privVals,
			evidence,
		)
		require.NoError(t, err, "height %d", height)
	}
}

func TestValidateFailBlockOnCommittedEvidence(t *testing.T) {
	var height int64 = 1
	state, stateDB, privVals := makeState(2, int(height))
	_, val := state.Validators.GetByIndex(0)
	_, val2 := state.Validators.GetByIndex(1)
	ev := types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultTestTime,
		privVals[val.Address.String()], chainID)
	ev2 := types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultTestTime,
		privVals[val2.Address.String()], chainID)

	header := &types.Header{Time: defaultTestTime}

	evpool := &mocks.EvidencePool{}
	evpool.On("IsPending", ev).Return(false)
	evpool.On("IsPending", ev2).Return(false)
	evpool.On("IsCommitted", ev).Return(false)
	evpool.On("IsCommitted", ev2).Return(true)
	evpool.On("Header", height).Return(header)

	blockExec := sm.NewBlockExecutor(
		stateDB, log.TestingLogger(),
		nil,
		nil,
		evpool)
	// A block with a couple pieces of evidence passes.
	block := makeBlock(state, height)
	block.Evidence.Evidence = []types.Evidence{ev, ev2}
	block.EvidenceHash = block.Evidence.Hash()
	err := blockExec.ValidateBlock(state, block)

	assert.Error(t, err)
	assert.IsType(t, err, &types.ErrEvidenceInvalid{})
}

func TestValidateAlreadyPendingEvidence(t *testing.T) {
	var height int64 = 1
	state, stateDB, privVals := makeState(2, int(height))
	_, val := state.Validators.GetByIndex(0)
	_, val2 := state.Validators.GetByIndex(1)
	ev := types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultTestTime,
		privVals[val.Address.String()], chainID)
	ev2 := types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultTestTime,
		privVals[val2.Address.String()], chainID)
	header := &types.Header{Time: defaultTestTime}

	evpool := &mocks.EvidencePool{}
	evpool.On("IsPending", ev).Return(false)
	evpool.On("IsPending", ev2).Return(true)
	evpool.On("IsCommitted", ev).Return(false)
	evpool.On("IsCommitted", ev2).Return(false)
	evpool.On("Header", height).Return(header)

	blockExec := sm.NewBlockExecutor(
		stateDB, log.TestingLogger(),
		nil,
		nil,
		evpool)
	// A block with a couple pieces of evidence passes.
	block := makeBlock(state, height)
	// add one evidence seen before and one evidence that hasn't
	block.Evidence.Evidence = []types.Evidence{ev, ev2}
	block.EvidenceHash = block.Evidence.Hash()
	err := blockExec.ValidateBlock(state, block)

	assert.NoError(t, err)
}

func TestValidateDuplicateEvidenceShouldFail(t *testing.T) {
	var height int64 = 1
	state, stateDB, privVals := makeState(2, int(height))
	_, val := state.Validators.GetByIndex(0)
	_, val2 := state.Validators.GetByIndex(1)
	ev := types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultTestTime,
		privVals[val.Address.String()], chainID)
	ev2 := types.NewMockDuplicateVoteEvidenceWithValidator(height, defaultTestTime,
		privVals[val2.Address.String()], chainID)

	blockExec := sm.NewBlockExecutor(
		stateDB, log.TestingLogger(),
		nil,
		nil,
		sm.MockEvidencePool{})
	// A block with a couple pieces of evidence passes.
	block := makeBlock(state, height)
	block.Evidence.Evidence = []types.Evidence{ev, ev2, ev2}
	block.EvidenceHash = block.Evidence.Hash()
	err := blockExec.ValidateBlock(state, block)

	assert.Error(t, err)
}

var (
	blockID = types.BlockID{
		Hash: tmrand.Bytes(tmhash.Size),
		PartSetHeader: types.PartSetHeader{
			Total: 1,
			Hash:  tmrand.Bytes(tmhash.Size),
		},
	}
	differentBlockID = types.BlockID{
		Hash: tmrand.Bytes(tmhash.Size),
		PartSetHeader: types.PartSetHeader{
			Total: 1,
			Hash:  tmrand.Bytes(tmhash.Size),
		},
	}
)

func TestValidateUnseenAmnesiaEvidence(t *testing.T) {
	var height int64 = 1
	state, stateDB, vals := makeState(1, int(height))
	addr, val := state.Validators.GetByIndex(0)
	voteA := makeVote(height, 1, 0, addr, blockID)
	vA := voteA.ToProto()
	err := vals[val.Address.String()].SignVote(chainID, vA)
	voteA.Signature = vA.Signature
	require.NoError(t, err)
	voteB := makeVote(height, 2, 0, addr, differentBlockID)
	vB := voteB.ToProto()
	err = vals[val.Address.String()].SignVote(chainID, vB)
	voteB.Signature = vB.Signature
	require.NoError(t, err)
	pe := &types.PotentialAmnesiaEvidence{
		VoteA: voteA,
		VoteB: voteB,
	}
	ae := &types.AmnesiaEvidence{
		PotentialAmnesiaEvidence: pe,
		Polc:                     types.NewEmptyPOLC(),
	}

	evpool := &mocks.EvidencePool{}
	evpool.On("IsPending", ae).Return(false)
	evpool.On("IsCommitted", ae).Return(false)
	evpool.On("AddEvidence", ae).Return(nil)
	evpool.On("AddEvidence", pe).Return(nil)

	blockExec := sm.NewBlockExecutor(
		stateDB, log.TestingLogger(),
		nil,
		nil,
		evpool)
	// A block with a couple pieces of evidence passes.
	block := makeBlock(state, height)
	block.Evidence.Evidence = []types.Evidence{ae}
	block.EvidenceHash = block.Evidence.Hash()
	err = blockExec.ValidateBlock(state, block)
	// if we don't have this evidence and it is has an empty polc then we expect to
	// start our own trial period first
	errMsg := "Invalid evidence: amnesia evidence is new and hasn't undergone trial period yet."
	if assert.Error(t, err) {
		assert.Equal(t, errMsg, err.Error()[:len(errMsg)])
	}
}

// Amnesia Evidence can be directly approved without needing to undergo the trial period
func TestValidatePrimedAmnesiaEvidence(t *testing.T) {
	var height int64 = 1
	state, stateDB, vals := makeState(1, int(height))
	addr, val := state.Validators.GetByIndex(0)
	voteA := makeVote(height, 1, 0, addr, blockID)
	voteA.Timestamp = defaultTestTime.Add(1 * time.Minute)
	vA := voteA.ToProto()
	err := vals[val.Address.String()].SignVote(chainID, vA)
	require.NoError(t, err)
	voteA.Signature = vA.Signature
	voteB := makeVote(height, 2, 0, addr, differentBlockID)
	voteB.Timestamp = defaultTestTime
	vB := voteB.ToProto()
	err = vals[val.Address.String()].SignVote(chainID, vB)
	voteB.Signature = vB.Signature
	require.NoError(t, err)
	pe := types.NewPotentialAmnesiaEvidence(voteB, voteA, defaultTestTime)
	ae := &types.AmnesiaEvidence{
		PotentialAmnesiaEvidence: pe,
		Polc:                     types.NewEmptyPOLC(),
	}
	header := &types.Header{Time: defaultTestTime}

	evpool := &mocks.EvidencePool{}
	evpool.On("IsPending", ae).Return(false)
	evpool.On("IsCommitted", ae).Return(false)
	evpool.On("AddEvidence", ae).Return(nil)
	evpool.On("AddEvidence", pe).Return(nil)
	evpool.On("Header", height).Return(header)

	blockExec := sm.NewBlockExecutor(
		stateDB, log.TestingLogger(),
		nil,
		nil,
		evpool)
	// A block with a couple pieces of evidence passes.
	block := makeBlock(state, height)
	block.Evidence.Evidence = []types.Evidence{ae}
	block.EvidenceHash = block.Evidence.Hash()
	err = blockExec.ValidateBlock(state, block)
	// No error because this type of amnesia evidence is punishable
	// without the need of a trial period
	assert.NoError(t, err)
}

func TestVerifyEvidenceWrongAddress(t *testing.T) {
	var height int64 = 1
	state, stateDB, _ := makeState(1, int(height))
	ev := types.NewMockDuplicateVoteEvidence(height, defaultTestTime, chainID)

	header := &types.Header{Time: defaultTestTime}

	evpool := &mocks.EvidencePool{}
	evpool.On("IsPending", ev).Return(false)
	evpool.On("IsCommitted", ev).Return(false)
	evpool.On("Header", height).Return(header)

	blockExec := sm.NewBlockExecutor(
		stateDB, log.TestingLogger(),
		nil,
		nil,
		evpool,
	)
	// A block with a couple pieces of evidence passes.
	block := makeBlock(state, height)
	block.Evidence.Evidence = []types.Evidence{ev}
	block.EvidenceHash = block.Evidence.Hash()
	err := blockExec.ValidateBlock(state, block)
	errMsg := "Invalid evidence: address "
	if assert.Error(t, err) {
		assert.Equal(t, err.Error()[:len(errMsg)], errMsg)
	}
}

func TestVerifyEvidenceExpiredEvidence(t *testing.T) {
	var height int64 = 4
	state, stateDB, _ := makeState(1, int(height))
	state.ConsensusParams.Evidence.MaxAgeNumBlocks = 1
	ev := types.NewMockDuplicateVoteEvidence(1, defaultTestTime, chainID)
	err := sm.VerifyEvidence(stateDB, state, ev, &types.Header{Time: defaultTestTime})
	errMsg := "evidence from height 1 (created at: 2019-01-01 00:00:00 +0000 UTC) is too old"
	if assert.Error(t, err) {
		assert.Equal(t, err.Error()[:len(errMsg)], errMsg)
	}
}

func TestVerifyEvidenceInvalidTime(t *testing.T) {
	height := 4
	state, stateDB, _ := makeState(1, height)
	differentTime := time.Date(2019, 2, 1, 0, 0, 0, 0, time.UTC)
	ev := types.NewMockDuplicateVoteEvidence(int64(height), differentTime, chainID)
	err := sm.VerifyEvidence(stateDB, state, ev, &types.Header{Time: defaultTestTime})
	errMsg := "evidence time (2019-02-01 00:00:00 +0000 UTC) is different to the time" +
		" of the header we have for the same height (2019-01-01 00:00:00 +0000 UTC)"
	if assert.Error(t, err) {
		assert.Equal(t, errMsg, err.Error())
	}
}

func TestVerifyEvidenceWithAmnesiaEvidence(t *testing.T) {
	var height int64 = 1
	state, stateDB, vals := makeState(4, int(height))
	addr, val := state.Validators.GetByIndex(0)
	addr2, val2 := state.Validators.GetByIndex(1)
	voteA := makeVote(height, 1, 0, addr, types.BlockID{})
	vA := voteA.ToProto()
	err := vals[val.Address.String()].SignVote(chainID, vA)
	voteA.Signature = vA.Signature
	require.NoError(t, err)
	voteB := makeVote(height, 2, 0, addr, blockID)
	vB := voteB.ToProto()
	err = vals[val.Address.String()].SignVote(chainID, vB)
	voteB.Signature = vB.Signature
	require.NoError(t, err)

	pae := types.NewPotentialAmnesiaEvidence(voteA, voteB, defaultTestTime)

	voteC := makeVote(height, 2, 1, addr2, blockID)
	vC := voteC.ToProto()
	err = vals[val2.Address.String()].SignVote(chainID, vC)
	voteC.Signature = vC.Signature
	require.NoError(t, err)
	//var ae types.Evidence
	badAe := &types.AmnesiaEvidence{
		PotentialAmnesiaEvidence: pae,
		Polc: &types.ProofOfLockChange{
			Votes:  []*types.Vote{voteC},
			PubKey: val.PubKey,
		},
	}
	err = sm.VerifyEvidence(stateDB, state, badAe, &types.Header{Time: defaultTestTime})
	if assert.Error(t, err) {
		assert.Equal(t, err.Error(), "amnesia evidence contains invalid polc, err: "+
			"invalid commit -- insufficient voting power: got 1000, needed more than 2667")
	}
	addr3, val3 := state.Validators.GetByIndex(2)
	voteD := makeVote(height, 2, 2, addr3, blockID)
	vD := voteD.ToProto()
	err = vals[val3.Address.String()].SignVote(chainID, vD)
	require.NoError(t, err)
	voteD.Signature = vD.Signature
	addr4, val4 := state.Validators.GetByIndex(3)
	voteE := makeVote(height, 2, 3, addr4, blockID)
	vE := voteE.ToProto()
	err = vals[val4.Address.String()].SignVote(chainID, vE)
	voteE.Signature = vE.Signature
	require.NoError(t, err)

	goodAe := &types.AmnesiaEvidence{
		PotentialAmnesiaEvidence: pae,
		Polc: &types.ProofOfLockChange{
			Votes:  []*types.Vote{voteC, voteD, voteE},
			PubKey: val.PubKey,
		},
	}
	err = sm.VerifyEvidence(stateDB, state, goodAe, &types.Header{Time: defaultTestTime})
	assert.NoError(t, err)

	goodAe = &types.AmnesiaEvidence{
		PotentialAmnesiaEvidence: pae,
		Polc:                     types.NewEmptyPOLC(),
	}
	err = sm.VerifyEvidence(stateDB, state, goodAe, &types.Header{Time: defaultTestTime})
	assert.NoError(t, err)

}

func TestVerifyEvidenceWithLunaticValidatorEvidence(t *testing.T) {
	state, stateDB, vals := makeState(4, 4)
	state.ConsensusParams.Evidence.MaxAgeNumBlocks = 1
	addr, val := state.Validators.GetByIndex(0)
	h := &types.Header{
		Version:            version.Consensus{Block: 1, App: 2},
		ChainID:            chainID,
		Height:             3,
		Time:               defaultTestTime,
		LastBlockID:        blockID,
		LastCommitHash:     tmhash.Sum([]byte("last_commit_hash")),
		DataHash:           tmhash.Sum([]byte("data_hash")),
		ValidatorsHash:     tmhash.Sum([]byte("validators_hash")),
		NextValidatorsHash: tmhash.Sum([]byte("next_validators_hash")),
		ConsensusHash:      tmhash.Sum([]byte("consensus_hash")),
		AppHash:            tmhash.Sum([]byte("app_hash")),
		LastResultsHash:    tmhash.Sum([]byte("last_results_hash")),
		EvidenceHash:       tmhash.Sum([]byte("evidence_hash")),
		ProposerAddress:    crypto.AddressHash([]byte("proposer_address")),
	}
	vote := makeVote(3, 1, 0, addr, types.BlockID{
		Hash: h.Hash(),
		PartSetHeader: types.PartSetHeader{
			Total: 100,
			Hash:  crypto.CRandBytes(tmhash.Size),
		},
	})
	v := vote.ToProto()
	err := vals[val.Address.String()].SignVote(chainID, v)
	vote.Signature = v.Signature
	require.NoError(t, err)
	ev := types.NewLunaticValidatorEvidence(h, vote, "ConsensusHash", defaultTestTime)
	err = ev.ValidateBasic()
	require.NoError(t, err)
	err = sm.VerifyEvidence(stateDB, state, ev, h)
	if assert.Error(t, err) {
		assert.Equal(t, "ConsensusHash matches committed hash", err.Error())
	}
}

func makeVote(height int64, round, index int32, addr bytes.HexBytes, blockID types.BlockID) *types.Vote {
	return &types.Vote{
		Type:             tmproto.SignedMsgType(2),
		Height:           height,
		Round:            round,
		BlockID:          blockID,
		Timestamp:        time.Now(),
		ValidatorAddress: addr,
		ValidatorIndex:   index,
	}
}
