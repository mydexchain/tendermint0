package types

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	gogotypes "github.com/gogo/protobuf/types"

	"github.com/mydexchain/tendermint0/crypto"
	"github.com/mydexchain/tendermint0/crypto/merkle"
	"github.com/mydexchain/tendermint0/crypto/tmhash"
	"github.com/mydexchain/tendermint0/libs/bits"
	tmbytes "github.com/mydexchain/tendermint0/libs/bytes"
	tmmath "github.com/mydexchain/tendermint0/libs/math"
	tmsync "github.com/mydexchain/tendermint0/libs/sync"
	tmproto "github.com/mydexchain/tendermint0/proto/tendermint/types"
	tmversion "github.com/mydexchain/tendermint0/proto/tendermint/version"
)

const (
	// MaxHeaderBytes is a maximum header size.
	MaxHeaderBytes int64 = 626

	// MaxOverheadForBlock - maximum overhead to encode a block (up to
	// MaxBlockSizeBytes in size) not including it's parts except Data.
	// This means it also excludes the overhead for individual transactions.
	//
	// Uvarint length of MaxBlockSizeBytes: 4 bytes
	// 2 fields (2 embedded):               2 bytes
	// Uvarint length of Data.Txs:          4 bytes
	// Data.Txs field:                      1 byte
	MaxOverheadForBlock int64 = 11
)

// Block defines the atomic unit of a Tendermint blockchain.
type Block struct {
	mtx tmsync.Mutex

	Header     `json:"header"`
	Data       `json:"data"`
	Evidence   EvidenceData `json:"evidence"`
	LastCommit *Commit      `json:"last_commit"`
}

// ValidateBasic performs basic validation that doesn't involve state data.
// It checks the internal consistency of the block.
// Further validation is done using state#ValidateBlock.
func (b *Block) ValidateBasic() error {
	if b == nil {
		return errors.New("nil block")
	}

	b.mtx.Lock()
	defer b.mtx.Unlock()

	if err := b.Header.ValidateBasic(); err != nil {
		return fmt.Errorf("invalid header: %w", err)
	}

	// Validate the last commit and its hash.
	if b.LastCommit == nil {
		return errors.New("nil LastCommit")
	}
	if err := b.LastCommit.ValidateBasic(); err != nil {
		return fmt.Errorf("wrong LastCommit: %v", err)
	}

	if !bytes.Equal(b.LastCommitHash, b.LastCommit.Hash()) {
		return fmt.Errorf("wrong Header.LastCommitHash. Expected %v, got %v",
			b.LastCommit.Hash(),
			b.LastCommitHash,
		)
	}

	// NOTE: b.Data.Txs may be nil, but b.Data.Hash() still works fine.
	if !bytes.Equal(b.DataHash, b.Data.Hash()) {
		return fmt.Errorf(
			"wrong Header.DataHash. Expected %v, got %v",
			b.Data.Hash(),
			b.DataHash,
		)
	}

	// NOTE: b.Evidence.Evidence may be nil, but we're just looping.
	for i, ev := range b.Evidence.Evidence {
		switch ev.(type) {
		case *ConflictingHeadersEvidence:
			// ConflictingHeadersEvidence must be broken up in pieces and never
			// committed as a single piece.
			return fmt.Errorf("found ConflictingHeadersEvidence (#%d)", i)
		case *PotentialAmnesiaEvidence:
			// PotentialAmnesiaEvidence does not contribute to anything on its own, so
			// reject it as well.
			return fmt.Errorf("found PotentialAmnesiaEvidence (#%d)", i)
		}

		if err := ev.ValidateBasic(); err != nil {
			return fmt.Errorf("invalid evidence (#%d): %v", i, err)
		}
	}

	if !bytes.Equal(b.EvidenceHash, b.Evidence.Hash()) {
		return fmt.Errorf("wrong Header.EvidenceHash. Expected %v, got %v",
			b.EvidenceHash,
			b.Evidence.Hash(),
		)
	}

	return nil
}

// fillHeader fills in any remaining header fields that are a function of the block data
func (b *Block) fillHeader() {
	if b.LastCommitHash == nil {
		b.LastCommitHash = b.LastCommit.Hash()
	}
	if b.DataHash == nil {
		b.DataHash = b.Data.Hash()
	}
	if b.EvidenceHash == nil {
		b.EvidenceHash = b.Evidence.Hash()
	}
}

// Hash computes and returns the block hash.
// If the block is incomplete, block hash is nil for safety.
func (b *Block) Hash() tmbytes.HexBytes {
	if b == nil {
		return nil
	}
	b.mtx.Lock()
	defer b.mtx.Unlock()

	if b.LastCommit == nil {
		return nil
	}
	b.fillHeader()
	return b.Header.Hash()
}

// MakePartSet returns a PartSet containing parts of a serialized block.
// This is the form in which the block is gossipped to peers.
// CONTRACT: partSize is greater than zero.
func (b *Block) MakePartSet(partSize uint32) *PartSet {
	if b == nil {
		return nil
	}
	b.mtx.Lock()
	defer b.mtx.Unlock()

	pbb, err := b.ToProto()
	if err != nil {
		panic(err)
	}
	bz, err := proto.Marshal(pbb)
	if err != nil {
		panic(err)
	}
	return NewPartSetFromData(bz, partSize)
}

// HashesTo is a convenience function that checks if a block hashes to the given argument.
// Returns false if the block is nil or the hash is empty.
func (b *Block) HashesTo(hash []byte) bool {
	if len(hash) == 0 {
		return false
	}
	if b == nil {
		return false
	}
	return bytes.Equal(b.Hash(), hash)
}

// Size returns size of the block in bytes.
func (b *Block) Size() int {
	pbb, err := b.ToProto()
	if err != nil {
		return 0
	}

	return pbb.Size()
}

// String returns a string representation of the block
//
// See StringIndented.
func (b *Block) String() string {
	return b.StringIndented("")
}

// StringIndented returns an indented String.
//
// Header
// Data
// Evidence
// LastCommit
// Hash
func (b *Block) StringIndented(indent string) string {
	if b == nil {
		return "nil-Block"
	}
	return fmt.Sprintf(`Block{
%s  %v
%s  %v
%s  %v
%s  %v
%s}#%v`,
		indent, b.Header.StringIndented(indent+"  "),
		indent, b.Data.StringIndented(indent+"  "),
		indent, b.Evidence.StringIndented(indent+"  "),
		indent, b.LastCommit.StringIndented(indent+"  "),
		indent, b.Hash())
}

// StringShort returns a shortened string representation of the block.
func (b *Block) StringShort() string {
	if b == nil {
		return "nil-Block"
	}
	return fmt.Sprintf("Block#%X", b.Hash())
}

// ToProto converts Block to protobuf
func (b *Block) ToProto() (*tmproto.Block, error) {
	if b == nil {
		return nil, errors.New("nil Block")
	}

	pb := new(tmproto.Block)

	pb.Header = *b.Header.ToProto()
	pb.LastCommit = b.LastCommit.ToProto()
	pb.Data = b.Data.ToProto()

	protoEvidence, err := b.Evidence.ToProto()
	if err != nil {
		return nil, err
	}
	pb.Evidence = *protoEvidence

	return pb, nil
}

// FromProto sets a protobuf Block to the given pointer.
// It returns an error if the block is invalid.
func BlockFromProto(bp *tmproto.Block) (*Block, error) {
	if bp == nil {
		return nil, errors.New("nil block")
	}

	b := new(Block)
	h, err := HeaderFromProto(&bp.Header)
	if err != nil {
		return nil, err
	}
	b.Header = h
	data, err := DataFromProto(&bp.Data)
	if err != nil {
		return nil, err
	}
	b.Data = data
	b.Evidence.FromProto(&bp.Evidence)

	if bp.LastCommit != nil {
		lc, err := CommitFromProto(bp.LastCommit)
		if err != nil {
			return nil, err
		}
		b.LastCommit = lc
	}

	return b, b.ValidateBasic()
}

//-----------------------------------------------------------------------------

// MaxDataBytes returns the maximum size of block's data.
//
// XXX: Panics on negative result.
func MaxDataBytes(maxBytes int64, valsCount, evidenceCount int) int64 {
	maxDataBytes := maxBytes -
		MaxOverheadForBlock -
		MaxHeaderBytes -
		int64(valsCount)*MaxVoteBytes -
		int64(evidenceCount)*MaxEvidenceBytes

	if maxDataBytes < 0 {
		panic(fmt.Sprintf(
			"Negative MaxDataBytes. Block.MaxBytes=%d is too small to accommodate header&lastCommit&evidence=%d",
			maxBytes,
			-(maxDataBytes - maxBytes),
		))
	}

	return maxDataBytes

}

// MaxDataBytesUnknownEvidence returns the maximum size of block's data when
// evidence count is unknown. MaxEvidencePerBlock will be used for the size
// of evidence.
//
// XXX: Panics on negative result.
func MaxDataBytesUnknownEvidence(maxBytes int64, valsCount int, maxNumEvidence uint32) int64 {
	maxEvidenceBytes := int64(maxNumEvidence) * MaxEvidenceBytes
	maxDataBytes := maxBytes -
		MaxOverheadForBlock -
		MaxHeaderBytes -
		int64(valsCount)*MaxVoteBytes -
		maxEvidenceBytes

	if maxDataBytes < 0 {
		panic(fmt.Sprintf(
			"Negative MaxDataBytesUnknownEvidence. Block.MaxBytes=%d is too small to accommodate header&lastCommit&evidence=%d",
			maxBytes,
			-(maxDataBytes - maxBytes),
		))
	}

	return maxDataBytes
}

//-----------------------------------------------------------------------------

// Header defines the structure of a Tendermint block header.
// NOTE: changes to the Header should be duplicated in:
// - header.Hash()
// - abci.Header
// - https://github.com/mydexchain/spec/blob/master/spec/blockchain/blockchain.md
type Header struct {
	// basic block info
	Version tmversion.Consensus `json:"version"`
	ChainID string              `json:"chain_id"`
	Height  int64               `json:"height"`
	Time    time.Time           `json:"time"`

	// prev block info
	LastBlockID BlockID `json:"last_block_id"`

	// hashes of block data
	LastCommitHash tmbytes.HexBytes `json:"last_commit_hash"` // commit from validators from the last block
	DataHash       tmbytes.HexBytes `json:"data_hash"`        // transactions

	// hashes from the app output from the prev block
	ValidatorsHash     tmbytes.HexBytes `json:"validators_hash"`      // validators for the current block
	NextValidatorsHash tmbytes.HexBytes `json:"next_validators_hash"` // validators for the next block
	ConsensusHash      tmbytes.HexBytes `json:"consensus_hash"`       // consensus params for current block
	AppHash            tmbytes.HexBytes `json:"app_hash"`             // state after txs from the previous block
	// root hash of all results from the txs from the previous block
	LastResultsHash tmbytes.HexBytes `json:"last_results_hash"`

	// consensus info
	EvidenceHash    tmbytes.HexBytes `json:"evidence_hash"`    // evidence included in the block
	ProposerAddress Address          `json:"proposer_address"` // original proposer of the block
}

// Populate the Header with state-derived data.
// Call this after MakeBlock to complete the Header.
func (h *Header) Populate(
	version tmversion.Consensus, chainID string,
	timestamp time.Time, lastBlockID BlockID,
	valHash, nextValHash []byte,
	consensusHash, appHash, lastResultsHash []byte,
	proposerAddress Address,
) {
	h.Version = version
	h.ChainID = chainID
	h.Time = timestamp
	h.LastBlockID = lastBlockID
	h.ValidatorsHash = valHash
	h.NextValidatorsHash = nextValHash
	h.ConsensusHash = consensusHash
	h.AppHash = appHash
	h.LastResultsHash = lastResultsHash
	h.ProposerAddress = proposerAddress
}

// ValidateBasic performs stateless validation on a Header returning an error
// if any validation fails.
//
// NOTE: Timestamp validation is subtle and handled elsewhere.
func (h Header) ValidateBasic() error {
	if len(h.ChainID) > MaxChainIDLen {
		return fmt.Errorf("chainID is too long; got: %d, max: %d", len(h.ChainID), MaxChainIDLen)
	}

	if h.Height < 0 {
		return errors.New("negative Height")
	} else if h.Height == 0 {
		return errors.New("zero Height")
	}

	if err := h.LastBlockID.ValidateBasic(); err != nil {
		return fmt.Errorf("wrong LastBlockID: %w", err)
	}

	if err := ValidateHash(h.LastCommitHash); err != nil {
		return fmt.Errorf("wrong LastCommitHash: %v", err)
	}

	if err := ValidateHash(h.DataHash); err != nil {
		return fmt.Errorf("wrong DataHash: %v", err)
	}

	if err := ValidateHash(h.EvidenceHash); err != nil {
		return fmt.Errorf("wrong EvidenceHash: %v", err)
	}

	if len(h.ProposerAddress) != crypto.AddressSize {
		return fmt.Errorf(
			"invalid ProposerAddress length; got: %d, expected: %d",
			len(h.ProposerAddress), crypto.AddressSize,
		)
	}

	// Basic validation of hashes related to application data.
	// Will validate fully against state in state#ValidateBlock.
	if err := ValidateHash(h.ValidatorsHash); err != nil {
		return fmt.Errorf("wrong ValidatorsHash: %v", err)
	}
	if err := ValidateHash(h.NextValidatorsHash); err != nil {
		return fmt.Errorf("wrong NextValidatorsHash: %v", err)
	}
	if err := ValidateHash(h.ConsensusHash); err != nil {
		return fmt.Errorf("wrong ConsensusHash: %v", err)
	}
	// NOTE: AppHash is arbitrary length
	if err := ValidateHash(h.LastResultsHash); err != nil {
		return fmt.Errorf("wrong LastResultsHash: %v", err)
	}

	return nil
}

// Hash returns the hash of the header.
// It computes a Merkle tree from the header fields
// ordered as they appear in the Header.
// Returns nil if ValidatorHash is missing,
// since a Header is not valid unless there is
// a ValidatorsHash (corresponding to the validator set).
func (h *Header) Hash() tmbytes.HexBytes {
	if h == nil || len(h.ValidatorsHash) == 0 {
		return nil
	}
	hbz, err := h.Version.Marshal()
	if err != nil {
		return nil
	}

	pbt, err := gogotypes.StdTimeMarshal(h.Time)
	if err != nil {
		return nil
	}

	pbbi := h.LastBlockID.ToProto()
	bzbi, err := pbbi.Marshal()
	if err != nil {
		return nil
	}
	return merkle.HashFromByteSlices([][]byte{
		hbz,
		cdcEncode(h.ChainID),
		cdcEncode(h.Height),
		pbt,
		bzbi,
		cdcEncode(h.LastCommitHash),
		cdcEncode(h.DataHash),
		cdcEncode(h.ValidatorsHash),
		cdcEncode(h.NextValidatorsHash),
		cdcEncode(h.ConsensusHash),
		cdcEncode(h.AppHash),
		cdcEncode(h.LastResultsHash),
		cdcEncode(h.EvidenceHash),
		cdcEncode(h.ProposerAddress),
	})
}

// StringIndented returns an indented string representation of the header.
func (h *Header) StringIndented(indent string) string {
	if h == nil {
		return "nil-Header"
	}
	return fmt.Sprintf(`Header{
%s  Version:        %v
%s  ChainID:        %v
%s  Height:         %v
%s  Time:           %v
%s  LastBlockID:    %v
%s  LastCommit:     %v
%s  Data:           %v
%s  Validators:     %v
%s  NextValidators: %v
%s  App:            %v
%s  Consensus:      %v
%s  Results:        %v
%s  Evidence:       %v
%s  Proposer:       %v
%s}#%v`,
		indent, h.Version,
		indent, h.ChainID,
		indent, h.Height,
		indent, h.Time,
		indent, h.LastBlockID,
		indent, h.LastCommitHash,
		indent, h.DataHash,
		indent, h.ValidatorsHash,
		indent, h.NextValidatorsHash,
		indent, h.AppHash,
		indent, h.ConsensusHash,
		indent, h.LastResultsHash,
		indent, h.EvidenceHash,
		indent, h.ProposerAddress,
		indent, h.Hash())
}

// ToProto converts Header to protobuf
func (h *Header) ToProto() *tmproto.Header {
	if h == nil {
		return nil
	}

	return &tmproto.Header{
		Version:            h.Version,
		ChainID:            h.ChainID,
		Height:             h.Height,
		Time:               h.Time,
		LastBlockId:        h.LastBlockID.ToProto(),
		ValidatorsHash:     h.ValidatorsHash,
		NextValidatorsHash: h.NextValidatorsHash,
		ConsensusHash:      h.ConsensusHash,
		AppHash:            h.AppHash,
		DataHash:           h.DataHash,
		EvidenceHash:       h.EvidenceHash,
		LastResultsHash:    h.LastResultsHash,
		LastCommitHash:     h.LastCommitHash,
		ProposerAddress:    h.ProposerAddress,
	}
}

// FromProto sets a protobuf Header to the given pointer.
// It returns an error if the header is invalid.
func HeaderFromProto(ph *tmproto.Header) (Header, error) {
	if ph == nil {
		return Header{}, errors.New("nil Header")
	}

	h := new(Header)

	bi, err := BlockIDFromProto(&ph.LastBlockId)
	if err != nil {
		return Header{}, err
	}

	h.Version = ph.Version
	h.ChainID = ph.ChainID
	h.Height = ph.Height
	h.Time = ph.Time
	h.Height = ph.Height
	h.LastBlockID = *bi
	h.ValidatorsHash = ph.ValidatorsHash
	h.NextValidatorsHash = ph.NextValidatorsHash
	h.ConsensusHash = ph.ConsensusHash
	h.AppHash = ph.AppHash
	h.DataHash = ph.DataHash
	h.EvidenceHash = ph.EvidenceHash
	h.LastResultsHash = ph.LastResultsHash
	h.LastCommitHash = ph.LastCommitHash
	h.ProposerAddress = ph.ProposerAddress

	return *h, h.ValidateBasic()
}

//-------------------------------------

// BlockIDFlag indicates which BlockID the signature is for.
type BlockIDFlag byte

const (
	// BlockIDFlagAbsent - no vote was received from a validator.
	BlockIDFlagAbsent BlockIDFlag = iota + 1
	// BlockIDFlagCommit - voted for the Commit.BlockID.
	BlockIDFlagCommit
	// BlockIDFlagNil - voted for nil.
	BlockIDFlagNil
)

// CommitSig is a part of the Vote included in a Commit.
type CommitSig struct {
	BlockIDFlag      BlockIDFlag `json:"block_id_flag"`
	ValidatorAddress Address     `json:"validator_address"`
	Timestamp        time.Time   `json:"timestamp"`
	Signature        []byte      `json:"signature"`
}

// NewCommitSigForBlock returns new CommitSig with BlockIDFlagCommit.
func NewCommitSigForBlock(signature []byte, valAddr Address, ts time.Time) CommitSig {
	return CommitSig{
		BlockIDFlag:      BlockIDFlagCommit,
		ValidatorAddress: valAddr,
		Timestamp:        ts,
		Signature:        signature,
	}
}

// ForBlock returns true if CommitSig is for the block.
func (cs CommitSig) ForBlock() bool {
	return cs.BlockIDFlag == BlockIDFlagCommit
}

// NewCommitSigAbsent returns new CommitSig with BlockIDFlagAbsent. Other
// fields are all empty.
func NewCommitSigAbsent() CommitSig {
	return CommitSig{
		BlockIDFlag: BlockIDFlagAbsent,
	}
}

// Absent returns true if CommitSig is absent.
func (cs CommitSig) Absent() bool {
	return cs.BlockIDFlag == BlockIDFlagAbsent
}

// CommitSig returns a string representation of CommitSig.
//
// 1. first 6 bytes of signature
// 2. first 6 bytes of validator address
// 3. block ID flag
// 4. timestamp
func (cs CommitSig) String() string {
	return fmt.Sprintf("CommitSig{%X by %X on %v @ %s}",
		tmbytes.Fingerprint(cs.Signature),
		tmbytes.Fingerprint(cs.ValidatorAddress),
		cs.BlockIDFlag,
		CanonicalTime(cs.Timestamp))
}

// BlockID returns the Commit's BlockID if CommitSig indicates signing,
// otherwise - empty BlockID.
func (cs CommitSig) BlockID(commitBlockID BlockID) BlockID {
	var blockID BlockID
	switch cs.BlockIDFlag {
	case BlockIDFlagAbsent:
		blockID = BlockID{}
	case BlockIDFlagCommit:
		blockID = commitBlockID
	case BlockIDFlagNil:
		blockID = BlockID{}
	default:
		panic(fmt.Sprintf("Unknown BlockIDFlag: %v", cs.BlockIDFlag))
	}
	return blockID
}

// ValidateBasic performs basic validation.
func (cs CommitSig) ValidateBasic() error {
	switch cs.BlockIDFlag {
	case BlockIDFlagAbsent:
	case BlockIDFlagCommit:
	case BlockIDFlagNil:
	default:
		return fmt.Errorf("unknown BlockIDFlag: %v", cs.BlockIDFlag)
	}

	switch cs.BlockIDFlag {
	case BlockIDFlagAbsent:
		if len(cs.ValidatorAddress) != 0 {
			return errors.New("validator address is present")
		}
		if !cs.Timestamp.IsZero() {
			return errors.New("time is present")
		}
		if len(cs.Signature) != 0 {
			return errors.New("signature is present")
		}
	default:
		if len(cs.ValidatorAddress) != crypto.AddressSize {
			return fmt.Errorf("expected ValidatorAddress size to be %d bytes, got %d bytes",
				crypto.AddressSize,
				len(cs.ValidatorAddress),
			)
		}
		// NOTE: Timestamp validation is subtle and handled elsewhere.
		if len(cs.Signature) == 0 {
			return errors.New("signature is missing")
		}
		if len(cs.Signature) > MaxSignatureSize {
			return fmt.Errorf("signature is too big (max: %d)", MaxSignatureSize)
		}
	}

	return nil
}

// ToProto converts CommitSig to protobuf
func (cs *CommitSig) ToProto() *tmproto.CommitSig {
	if cs == nil {
		return nil
	}

	return &tmproto.CommitSig{
		BlockIdFlag:      tmproto.BlockIDFlag(cs.BlockIDFlag),
		ValidatorAddress: cs.ValidatorAddress,
		Timestamp:        cs.Timestamp,
		Signature:        cs.Signature,
	}
}

// FromProto sets a protobuf CommitSig to the given pointer.
// It returns an error if the CommitSig is invalid.
func (cs *CommitSig) FromProto(csp tmproto.CommitSig) error {

	cs.BlockIDFlag = BlockIDFlag(csp.BlockIdFlag)
	cs.ValidatorAddress = csp.ValidatorAddress
	cs.Timestamp = csp.Timestamp
	cs.Signature = csp.Signature

	return cs.ValidateBasic()
}

//-------------------------------------

// Commit contains the evidence that a block was committed by a set of validators.
// NOTE: Commit is empty for height 1, but never nil.
type Commit struct {
	// NOTE: The signatures are in order of address to preserve the bonded
	// ValidatorSet order.
	// Any peer with a block can gossip signatures by index with a peer without
	// recalculating the active ValidatorSet.
	Height     int64       `json:"height"`
	Round      int32       `json:"round"`
	BlockID    BlockID     `json:"block_id"`
	Signatures []CommitSig `json:"signatures"`

	// Memoized in first call to corresponding method.
	// NOTE: can't memoize in constructor because constructor isn't used for
	// unmarshaling.
	hash     tmbytes.HexBytes
	bitArray *bits.BitArray
}

// NewCommit returns a new Commit.
func NewCommit(height int64, round int32, blockID BlockID, commitSigs []CommitSig) *Commit {
	return &Commit{
		Height:     height,
		Round:      round,
		BlockID:    blockID,
		Signatures: commitSigs,
	}
}

// CommitToVoteSet constructs a VoteSet from the Commit and validator set.
// Panics if signatures from the commit can't be added to the voteset.
// Inverse of VoteSet.MakeCommit().
func CommitToVoteSet(chainID string, commit *Commit, vals *ValidatorSet) *VoteSet {
	voteSet := NewVoteSet(chainID, commit.Height, commit.Round, tmproto.PrecommitType, vals)
	for idx, commitSig := range commit.Signatures {
		if commitSig.Absent() {
			continue // OK, some precommits can be missing.
		}
		added, err := voteSet.AddVote(commit.GetVote(int32(idx)))
		if !added || err != nil {
			panic(fmt.Sprintf("Failed to reconstruct LastCommit: %v", err))
		}
	}
	return voteSet
}

// GetVote converts the CommitSig for the given valIdx to a Vote.
// Returns nil if the precommit at valIdx is nil.
// Panics if valIdx >= commit.Size().
func (commit *Commit) GetVote(valIdx int32) *Vote {
	commitSig := commit.Signatures[valIdx]
	return &Vote{
		Type:             tmproto.PrecommitType,
		Height:           commit.Height,
		Round:            commit.Round,
		BlockID:          commitSig.BlockID(commit.BlockID),
		Timestamp:        commitSig.Timestamp,
		ValidatorAddress: commitSig.ValidatorAddress,
		ValidatorIndex:   valIdx,
		Signature:        commitSig.Signature,
	}
}

// VoteSignBytes returns the bytes of the Vote corresponding to valIdx for
// signing.
//
// The only unique part is the Timestamp - all other fields signed over are
// otherwise the same for all validators.
//
// Panics if valIdx >= commit.Size().
//
// See VoteSignBytes
func (commit *Commit) VoteSignBytes(chainID string, valIdx int32) []byte {
	v := commit.GetVote(valIdx).ToProto()
	return VoteSignBytes(chainID, v)
}

// Type returns the vote type of the commit, which is always VoteTypePrecommit
// Implements VoteSetReader.
func (commit *Commit) Type() byte {
	return byte(tmproto.PrecommitType)
}

// GetHeight returns height of the commit.
// Implements VoteSetReader.
func (commit *Commit) GetHeight() int64 {
	return commit.Height
}

// GetRound returns height of the commit.
// Implements VoteSetReader.
func (commit *Commit) GetRound() int32 {
	return commit.Round
}

// Size returns the number of signatures in the commit.
// Implements VoteSetReader.
func (commit *Commit) Size() int {
	if commit == nil {
		return 0
	}
	return len(commit.Signatures)
}

// BitArray returns a BitArray of which validators voted for BlockID or nil in this commit.
// Implements VoteSetReader.
func (commit *Commit) BitArray() *bits.BitArray {
	if commit.bitArray == nil {
		commit.bitArray = bits.NewBitArray(len(commit.Signatures))
		for i, commitSig := range commit.Signatures {
			// TODO: need to check the BlockID otherwise we could be counting conflicts,
			// not just the one with +2/3 !
			commit.bitArray.SetIndex(i, !commitSig.Absent())
		}
	}
	return commit.bitArray
}

// GetByIndex returns the vote corresponding to a given validator index.
// Panics if `index >= commit.Size()`.
// Implements VoteSetReader.
func (commit *Commit) GetByIndex(valIdx int32) *Vote {
	return commit.GetVote(valIdx)
}

// IsCommit returns true if there is at least one signature.
// Implements VoteSetReader.
func (commit *Commit) IsCommit() bool {
	return len(commit.Signatures) != 0
}

// ValidateBasic performs basic validation that doesn't involve state data.
// Does not actually check the cryptographic signatures.
func (commit *Commit) ValidateBasic() error {
	if commit.Height < 0 {
		return errors.New("negative Height")
	}
	if commit.Round < 0 {
		return errors.New("negative Round")
	}

	if commit.Height >= 1 {
		if commit.BlockID.IsZero() {
			return errors.New("commit cannot be for nil block")
		}

		if len(commit.Signatures) == 0 {
			return errors.New("no signatures in commit")
		}
		for i, commitSig := range commit.Signatures {
			if err := commitSig.ValidateBasic(); err != nil {
				return fmt.Errorf("wrong CommitSig #%d: %v", i, err)
			}
		}
	}
	return nil
}

// Hash returns the hash of the commit
func (commit *Commit) Hash() tmbytes.HexBytes {
	if commit == nil {
		return nil
	}
	if commit.hash == nil {
		bs := make([][]byte, len(commit.Signatures))
		for i, commitSig := range commit.Signatures {
			pbcs := commitSig.ToProto()
			bz, err := pbcs.Marshal()
			if err != nil {
				panic(err)
			}

			bs[i] = bz
		}
		commit.hash = merkle.HashFromByteSlices(bs)
	}
	return commit.hash
}

// StringIndented returns a string representation of the commit.
func (commit *Commit) StringIndented(indent string) string {
	if commit == nil {
		return "nil-Commit"
	}
	commitSigStrings := make([]string, len(commit.Signatures))
	for i, commitSig := range commit.Signatures {
		commitSigStrings[i] = commitSig.String()
	}
	return fmt.Sprintf(`Commit{
%s  Height:     %d
%s  Round:      %d
%s  BlockID:    %v
%s  Signatures:
%s    %v
%s}#%v`,
		indent, commit.Height,
		indent, commit.Round,
		indent, commit.BlockID,
		indent,
		indent, strings.Join(commitSigStrings, "\n"+indent+"    "),
		indent, commit.hash)
}

// ToProto converts Commit to protobuf
func (commit *Commit) ToProto() *tmproto.Commit {
	if commit == nil {
		return nil
	}

	c := new(tmproto.Commit)
	sigs := make([]tmproto.CommitSig, len(commit.Signatures))
	for i := range commit.Signatures {
		sigs[i] = *commit.Signatures[i].ToProto()
	}
	c.Signatures = sigs

	c.Height = commit.Height
	c.Round = commit.Round
	c.BlockID = commit.BlockID.ToProto()
	if commit.hash != nil {
		c.Hash = commit.hash
	}
	c.BitArray = commit.bitArray.ToProto()
	return c
}

// FromProto sets a protobuf Commit to the given pointer.
// It returns an error if the commit is invalid.
func CommitFromProto(cp *tmproto.Commit) (*Commit, error) {
	if cp == nil {
		return nil, errors.New("nil Commit")
	}

	var (
		commit   = new(Commit)
		bitArray *bits.BitArray
	)

	bi, err := BlockIDFromProto(&cp.BlockID)
	if err != nil {
		return nil, err
	}

	bitArray.FromProto(cp.BitArray)

	sigs := make([]CommitSig, len(cp.Signatures))
	for i := range cp.Signatures {
		if err := sigs[i].FromProto(cp.Signatures[i]); err != nil {
			return nil, err
		}
	}
	commit.Signatures = sigs

	commit.Height = cp.Height
	commit.Round = cp.Round
	commit.BlockID = *bi
	commit.hash = cp.Hash
	commit.bitArray = bitArray

	return commit, commit.ValidateBasic()
}

//-----------------------------------------------------------------------------

// SignedHeader is a header along with the commits that prove it.
// It is the basis of the light client.
type SignedHeader struct {
	*Header `json:"header"`

	Commit *Commit `json:"commit"`
}

// ValidateBasic does basic consistency checks and makes sure the header
// and commit are consistent.
//
// NOTE: This does not actually check the cryptographic signatures.  Make sure
// to use a Verifier to validate the signatures actually provide a
// significantly strong proof for this header's validity.
func (sh SignedHeader) ValidateBasic(chainID string) error {
	if sh.Header == nil {
		return errors.New("missing header")
	}
	if sh.Commit == nil {
		return errors.New("missing commit")
	}

	if err := sh.Header.ValidateBasic(); err != nil {
		return fmt.Errorf("invalid header: %w", err)
	}
	if err := sh.Commit.ValidateBasic(); err != nil {
		return fmt.Errorf("invalid commit: %w", err)
	}

	if sh.ChainID != chainID {
		return fmt.Errorf("header belongs to another chain %q, not %q", sh.ChainID, chainID)
	}

	// Make sure the header is consistent with the commit.
	if sh.Commit.Height != sh.Height {
		return fmt.Errorf("header and commit height mismatch: %d vs %d", sh.Height, sh.Commit.Height)
	}
	if hhash, chash := sh.Hash(), sh.Commit.BlockID.Hash; !bytes.Equal(hhash, chash) {
		return fmt.Errorf("commit signs block %X, header is block %X", chash, hhash)
	}

	return nil
}

// String returns a string representation of SignedHeader.
func (sh SignedHeader) String() string {
	return sh.StringIndented("")
}

// StringIndented returns an indented string representation of SignedHeader.
//
// Header
// Commit
func (sh SignedHeader) StringIndented(indent string) string {
	return fmt.Sprintf(`SignedHeader{
%s  %v
%s  %v
%s}`,
		indent, sh.Header.StringIndented(indent+"  "),
		indent, sh.Commit.StringIndented(indent+"  "),
		indent)
}

// ToProto converts SignedHeader to protobuf
func (sh *SignedHeader) ToProto() *tmproto.SignedHeader {
	if sh == nil {
		return nil
	}

	psh := new(tmproto.SignedHeader)
	if sh.Header != nil {
		psh.Header = sh.Header.ToProto()
	}
	if sh.Commit != nil {
		psh.Commit = sh.Commit.ToProto()
	}

	return psh
}

// FromProto sets a protobuf SignedHeader to the given pointer.
// It returns an error if the hader or the commit is invalid.
func SignedHeaderFromProto(shp *tmproto.SignedHeader) (*SignedHeader, error) {
	if shp == nil {
		return nil, errors.New("nil SignedHeader")
	}

	sh := new(SignedHeader)

	if shp.Header != nil {
		h, err := HeaderFromProto(shp.Header)
		if err != nil {
			return nil, err
		}
		sh.Header = &h
	}

	if shp.Commit != nil {
		c, err := CommitFromProto(shp.Commit)
		if err != nil {
			return nil, err
		}
		sh.Commit = c
	}

	return sh, nil
}

//-----------------------------------------------------------------------------

// Data contains the set of transactions included in the block
type Data struct {

	// Txs that will be applied by state @ block.Height+1.
	// NOTE: not all txs here are valid.  We're just agreeing on the order first.
	// This means that block.AppHash does not include these txs.
	Txs Txs `json:"txs"`

	// Volatile
	hash tmbytes.HexBytes
}

// Hash returns the hash of the data
func (data *Data) Hash() tmbytes.HexBytes {
	if data == nil {
		return (Txs{}).Hash()
	}
	if data.hash == nil {
		data.hash = data.Txs.Hash() // NOTE: leaves of merkle tree are TxIDs
	}
	return data.hash
}

// StringIndented returns an indented string representation of the transactions.
func (data *Data) StringIndented(indent string) string {
	if data == nil {
		return "nil-Data"
	}
	txStrings := make([]string, tmmath.MinInt(len(data.Txs), 21))
	for i, tx := range data.Txs {
		if i == 20 {
			txStrings[i] = fmt.Sprintf("... (%v total)", len(data.Txs))
			break
		}
		txStrings[i] = fmt.Sprintf("%X (%d bytes)", tx.Hash(), len(tx))
	}
	return fmt.Sprintf(`Data{
%s  %v
%s}#%v`,
		indent, strings.Join(txStrings, "\n"+indent+"  "),
		indent, data.hash)
}

// ToProto converts Data to protobuf
func (data *Data) ToProto() tmproto.Data {
	tp := new(tmproto.Data)

	if len(data.Txs) > 0 {
		txBzs := make([][]byte, len(data.Txs))
		for i := range data.Txs {
			txBzs[i] = data.Txs[i]
		}
		tp.Txs = txBzs
	}

	if data.hash != nil {
		tp.Hash = data.hash
	}

	return *tp
}

// DataFromProto takes a protobuf representation of Data &
// returns the native type.
func DataFromProto(dp *tmproto.Data) (Data, error) {
	if dp == nil {
		return Data{}, errors.New("nil data")
	}
	data := new(Data)

	if len(dp.Txs) > 0 {
		txBzs := make(Txs, len(dp.Txs))
		for i := range dp.Txs {
			txBzs[i] = Tx(dp.Txs[i])
		}
		data.Txs = txBzs
	} else {
		data.Txs = Txs{}
	}

	data.hash = dp.Hash

	return *data, nil
}

//-----------------------------------------------------------------------------

// EvidenceData contains any evidence of malicious wrong-doing by validators
type EvidenceData struct {
	Evidence EvidenceList `json:"evidence"`

	// Volatile
	hash tmbytes.HexBytes
}

// Hash returns the hash of the data.
func (data *EvidenceData) Hash() tmbytes.HexBytes {
	if data.hash == nil {
		data.hash = data.Evidence.Hash()
	}
	return data.hash
}

// StringIndented returns a string representation of the evidence.
func (data *EvidenceData) StringIndented(indent string) string {
	if data == nil {
		return "nil-Evidence"
	}
	evStrings := make([]string, tmmath.MinInt(len(data.Evidence), 21))
	for i, ev := range data.Evidence {
		if i == 20 {
			evStrings[i] = fmt.Sprintf("... (%v total)", len(data.Evidence))
			break
		}
		evStrings[i] = fmt.Sprintf("Evidence:%v", ev)
	}
	return fmt.Sprintf(`EvidenceData{
%s  %v
%s}#%v`,
		indent, strings.Join(evStrings, "\n"+indent+"  "),
		indent, data.hash)
}

// ToProto converts EvidenceData to protobuf
func (data *EvidenceData) ToProto() (*tmproto.EvidenceData, error) {
	if data == nil {
		return nil, errors.New("nil evidence data")
	}

	evi := new(tmproto.EvidenceData)
	eviBzs := make([]tmproto.Evidence, len(data.Evidence))
	for i := range data.Evidence {
		protoEvi, err := EvidenceToProto(data.Evidence[i])
		if err != nil {
			return nil, err
		}
		eviBzs[i] = *protoEvi
	}
	evi.Evidence = eviBzs

	if data.hash != nil {
		evi.Hash = data.hash
	}

	return evi, nil
}

// FromProto sets a protobuf EvidenceData to the given pointer.
func (data *EvidenceData) FromProto(eviData *tmproto.EvidenceData) error {
	if eviData == nil {
		return errors.New("nil evidenceData")
	}

	eviBzs := make(EvidenceList, len(eviData.Evidence))
	for i := range eviData.Evidence {
		evi, err := EvidenceFromProto(&eviData.Evidence[i])
		if err != nil {
			return err
		}
		eviBzs[i] = evi
	}
	data.Evidence = eviBzs

	data.hash = eviData.GetHash()

	return nil
}

//--------------------------------------------------------------------------------

// BlockID
type BlockID struct {
	Hash          tmbytes.HexBytes `json:"hash"`
	PartSetHeader PartSetHeader    `json:"parts"`
}

// Equals returns true if the BlockID matches the given BlockID
func (blockID BlockID) Equals(other BlockID) bool {
	return bytes.Equal(blockID.Hash, other.Hash) &&
		blockID.PartSetHeader.Equals(other.PartSetHeader)
}

// Key returns a machine-readable string representation of the BlockID
func (blockID BlockID) Key() string {
	pbph := blockID.PartSetHeader.ToProto()
	bz, err := pbph.Marshal()
	if err != nil {
		panic(err)
	}

	return string(blockID.Hash) + string(bz)
}

// ValidateBasic performs basic validation.
func (blockID BlockID) ValidateBasic() error {
	// Hash can be empty in case of POLBlockID in Proposal.
	if err := ValidateHash(blockID.Hash); err != nil {
		return fmt.Errorf("wrong Hash")
	}
	if err := blockID.PartSetHeader.ValidateBasic(); err != nil {
		return fmt.Errorf("wrong PartSetHeader: %v", err)
	}
	return nil
}

// IsZero returns true if this is the BlockID of a nil block.
func (blockID BlockID) IsZero() bool {
	return len(blockID.Hash) == 0 &&
		blockID.PartSetHeader.IsZero()
}

// IsComplete returns true if this is a valid BlockID of a non-nil block.
func (blockID BlockID) IsComplete() bool {
	return len(blockID.Hash) == tmhash.Size &&
		blockID.PartSetHeader.Total > 0 &&
		len(blockID.PartSetHeader.Hash) == tmhash.Size
}

// String returns a human readable string representation of the BlockID.
//
// 1. hash
// 2. part set header
//
// See PartSetHeader#String
func (blockID BlockID) String() string {
	return fmt.Sprintf(`%v:%v`, blockID.Hash, blockID.PartSetHeader)
}

// ToProto converts BlockID to protobuf
func (blockID *BlockID) ToProto() tmproto.BlockID {
	if blockID == nil {
		return tmproto.BlockID{}
	}

	return tmproto.BlockID{
		Hash:          blockID.Hash,
		PartSetHeader: blockID.PartSetHeader.ToProto(),
	}
}

// FromProto sets a protobuf BlockID to the given pointer.
// It returns an error if the block id is invalid.
func BlockIDFromProto(bID *tmproto.BlockID) (*BlockID, error) {
	if bID == nil {
		return nil, errors.New("nil BlockID")
	}

	blockID := new(BlockID)
	ph, err := PartSetHeaderFromProto(&bID.PartSetHeader)
	if err != nil {
		return nil, err
	}

	blockID.PartSetHeader = *ph
	blockID.Hash = bID.Hash

	return blockID, blockID.ValidateBasic()
}
