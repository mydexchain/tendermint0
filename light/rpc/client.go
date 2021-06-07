package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"

	abci "github.com/mydexchain/tendermint0/abci/types"
	"github.com/mydexchain/tendermint0/crypto/merkle"
	tmbytes "github.com/mydexchain/tendermint0/libs/bytes"
	service "github.com/mydexchain/tendermint0/libs/service"
	light "github.com/mydexchain/tendermint0/light"
	rpcclient "github.com/mydexchain/tendermint0/rpc/client"
	ctypes "github.com/mydexchain/tendermint0/rpc/core/types"
	rpctypes "github.com/mydexchain/tendermint0/rpc/jsonrpc/types"
	"github.com/mydexchain/tendermint0/types"
)

var errNegOrZeroHeight = errors.New("negative or zero height")

// Client is an RPC client, which uses light#Client to verify data (if it can be
// proved!).
type Client struct {
	service.BaseService

	next rpcclient.Client
	lc   *light.Client
	prt  *merkle.ProofRuntime
}

var _ rpcclient.Client = (*Client)(nil)

// NewClient returns a new client.
func NewClient(next rpcclient.Client, lc *light.Client) *Client {
	c := &Client{
		next: next,
		lc:   lc,
		prt:  defaultProofRuntime(),
	}
	c.BaseService = *service.NewBaseService(nil, "Client", c)
	return c
}

func (c *Client) OnStart() error {
	if !c.next.IsRunning() {
		return c.next.Start()
	}
	return nil
}

func (c *Client) OnStop() {
	if c.next.IsRunning() {
		c.next.Stop()
	}
}

func (c *Client) Status() (*ctypes.ResultStatus, error) {
	return c.next.Status()
}

func (c *Client) ABCIInfo() (*ctypes.ResultABCIInfo, error) {
	return c.next.ABCIInfo()
}

func (c *Client) ABCIQuery(path string, data tmbytes.HexBytes) (*ctypes.ResultABCIQuery, error) {
	return c.ABCIQueryWithOptions(path, data, rpcclient.DefaultABCIQueryOptions)
}

// GetWithProofOptions is useful if you want full access to the ABCIQueryOptions.
// XXX Usage of path?  It's not used, and sometimes it's /, sometimes /key, sometimes /store.
func (c *Client) ABCIQueryWithOptions(path string, data tmbytes.HexBytes,
	opts rpcclient.ABCIQueryOptions) (*ctypes.ResultABCIQuery, error) {

	res, err := c.next.ABCIQueryWithOptions(path, data, opts)
	if err != nil {
		return nil, err
	}
	resp := res.Response

	// Validate the response.
	if resp.IsErr() {
		return nil, fmt.Errorf("err response code: %v", resp.Code)
	}
	if len(resp.Key) == 0 || resp.ProofOps == nil {
		return nil, errors.New("empty tree")
	}
	if resp.Height <= 0 {
		return nil, errNegOrZeroHeight
	}

	// Update the light client if we're behind.
	// NOTE: AppHash for height H is in header H+1.
	h, err := c.updateLightClientIfNeededTo(resp.Height + 1)
	if err != nil {
		return nil, err
	}

	// Validate the value proof against the trusted header.
	if resp.Value != nil {
		// Value exists
		// XXX How do we encode the key into a string...
		storeName, err := parseQueryStorePath(path)
		if err != nil {
			return nil, err
		}
		kp := merkle.KeyPath{}
		kp = kp.AppendKey([]byte(storeName), merkle.KeyEncodingURL)
		kp = kp.AppendKey(resp.Key, merkle.KeyEncodingURL)
		err = c.prt.VerifyValue(resp.ProofOps, h.AppHash, kp.String(), resp.Value)
		if err != nil {
			return nil, fmt.Errorf("verify value proof: %w", err)
		}
		return &ctypes.ResultABCIQuery{Response: resp}, nil
	}

	// OR validate the ansence proof against the trusted header.
	// XXX How do we encode the key into a string...
	err = c.prt.VerifyAbsence(resp.ProofOps, h.AppHash, string(resp.Key))
	if err != nil {
		return nil, fmt.Errorf("verify absence proof: %w", err)
	}
	return &ctypes.ResultABCIQuery{Response: resp}, nil
}

func (c *Client) BroadcastTxCommit(tx types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	return c.next.BroadcastTxCommit(tx)
}

func (c *Client) BroadcastTxAsync(tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	return c.next.BroadcastTxAsync(tx)
}

func (c *Client) BroadcastTxSync(tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	return c.next.BroadcastTxSync(tx)
}

func (c *Client) UnconfirmedTxs(limit *int) (*ctypes.ResultUnconfirmedTxs, error) {
	return c.next.UnconfirmedTxs(limit)
}

func (c *Client) NumUnconfirmedTxs() (*ctypes.ResultUnconfirmedTxs, error) {
	return c.next.NumUnconfirmedTxs()
}

func (c *Client) CheckTx(tx types.Tx) (*ctypes.ResultCheckTx, error) {
	return c.next.CheckTx(tx)
}

func (c *Client) NetInfo() (*ctypes.ResultNetInfo, error) {
	return c.next.NetInfo()
}

func (c *Client) DumpConsensusState() (*ctypes.ResultDumpConsensusState, error) {
	return c.next.DumpConsensusState()
}

func (c *Client) ConsensusState() (*ctypes.ResultConsensusState, error) {
	return c.next.ConsensusState()
}

func (c *Client) ConsensusParams(height *int64) (*ctypes.ResultConsensusParams, error) {
	res, err := c.next.ConsensusParams(height)
	if err != nil {
		return nil, err
	}

	// Validate res.
	if err := types.ValidateConsensusParams(res.ConsensusParams); err != nil {
		return nil, err
	}
	if res.BlockHeight <= 0 {
		return nil, errNegOrZeroHeight
	}

	// Update the light client if we're behind.
	h, err := c.updateLightClientIfNeededTo(res.BlockHeight)
	if err != nil {
		return nil, err
	}

	// Verify hash.
	if cH, tH := types.HashConsensusParams(res.ConsensusParams), h.ConsensusHash; !bytes.Equal(cH, tH) {
		return nil, fmt.Errorf("params hash %X does not match trusted hash %X",
			cH, tH)
	}

	return res, nil
}

func (c *Client) Health() (*ctypes.ResultHealth, error) {
	return c.next.Health()
}

// BlockchainInfo calls rpcclient#BlockchainInfo and then verifies every header
// returned.
func (c *Client) BlockchainInfo(minHeight, maxHeight int64) (*ctypes.ResultBlockchainInfo, error) {
	res, err := c.next.BlockchainInfo(minHeight, maxHeight)
	if err != nil {
		return nil, err
	}

	// Validate res.
	for i, meta := range res.BlockMetas {
		if meta == nil {
			return nil, fmt.Errorf("nil block meta %d", i)
		}
		if err := meta.ValidateBasic(); err != nil {
			return nil, fmt.Errorf("invalid block meta %d: %w", i, err)
		}
	}

	// Update the light client if we're behind.
	if len(res.BlockMetas) > 0 {
		lastHeight := res.BlockMetas[len(res.BlockMetas)-1].Header.Height
		if _, err := c.updateLightClientIfNeededTo(lastHeight); err != nil {
			return nil, err
		}
	}

	// Verify each of the BlockMetas.
	for _, meta := range res.BlockMetas {
		h, err := c.lc.TrustedHeader(meta.Header.Height)
		if err != nil {
			return nil, fmt.Errorf("trusted header %d: %w", meta.Header.Height, err)
		}
		if bmH, tH := meta.Header.Hash(), h.Hash(); !bytes.Equal(bmH, tH) {
			return nil, fmt.Errorf("block meta header %X does not match with trusted header %X",
				bmH, tH)
		}
	}

	return res, nil
}

func (c *Client) Genesis() (*ctypes.ResultGenesis, error) {
	return c.next.Genesis()
}

// Block calls rpcclient#Block and then verifies the result.
func (c *Client) Block(height *int64) (*ctypes.ResultBlock, error) {
	res, err := c.next.Block(height)
	if err != nil {
		return nil, err
	}

	// Validate res.
	if err := res.BlockID.ValidateBasic(); err != nil {
		return nil, err
	}
	if err := res.Block.ValidateBasic(); err != nil {
		return nil, err
	}
	if bmH, bH := res.BlockID.Hash, res.Block.Hash(); !bytes.Equal(bmH, bH) {
		return nil, fmt.Errorf("blockID %X does not match with block %X",
			bmH, bH)
	}

	// Update the light client if we're behind.
	h, err := c.updateLightClientIfNeededTo(res.Block.Height)
	if err != nil {
		return nil, err
	}

	// Verify block.
	if bH, tH := res.Block.Hash(), h.Hash(); !bytes.Equal(bH, tH) {
		return nil, fmt.Errorf("block header %X does not match with trusted header %X",
			bH, tH)
	}

	return res, nil
}

// BlockByHash calls rpcclient#BlockByHash and then verifies the result.
func (c *Client) BlockByHash(hash []byte) (*ctypes.ResultBlock, error) {
	res, err := c.next.BlockByHash(hash)
	if err != nil {
		return nil, err
	}

	// Validate res.
	if err := res.BlockID.ValidateBasic(); err != nil {
		return nil, err
	}
	if err := res.Block.ValidateBasic(); err != nil {
		return nil, err
	}
	if bmH, bH := res.BlockID.Hash, res.Block.Hash(); !bytes.Equal(bmH, bH) {
		return nil, fmt.Errorf("blockID %X does not match with block %X",
			bmH, bH)
	}

	// Update the light client if we're behind.
	h, err := c.updateLightClientIfNeededTo(res.Block.Height)
	if err != nil {
		return nil, err
	}

	// Verify block.
	if bH, tH := res.Block.Hash(), h.Hash(); !bytes.Equal(bH, tH) {
		return nil, fmt.Errorf("block header %X does not match with trusted header %X",
			bH, tH)
	}

	return res, nil
}

// BlockResults returns the block results for the given height. If no height is
// provided, the results of the block preceding the latest are returned.
func (c *Client) BlockResults(height *int64) (*ctypes.ResultBlockResults, error) {
	var h int64
	if height == nil {
		res, err := c.next.Status()
		if err != nil {
			return nil, fmt.Errorf("can't get latest height: %w", err)
		}
		// Can't return the latest block results here because we won't be able to
		// prove them. Return the results for the previous block instead.
		h = res.SyncInfo.LatestBlockHeight - 1
	} else {
		h = *height
	}

	res, err := c.next.BlockResults(&h)
	if err != nil {
		return nil, err
	}

	// Validate res.
	if res.Height <= 0 {
		return nil, errNegOrZeroHeight
	}

	// Update the light client if we're behind.
	trustedHeader, err := c.updateLightClientIfNeededTo(h + 1)
	if err != nil {
		return nil, err
	}

	// proto-encode BeginBlock events
	bbeBytes, err := proto.Marshal(&abci.ResponseBeginBlock{
		Events: res.BeginBlockEvents,
	})
	if err != nil {
		return nil, err
	}

	// Build a Merkle tree of proto-encoded DeliverTx results and get a hash.
	results := types.NewResults(res.TxsResults)

	// proto-encode EndBlock events.
	ebeBytes, err := proto.Marshal(&abci.ResponseEndBlock{
		Events: res.EndBlockEvents,
	})
	if err != nil {
		return nil, err
	}

	// Build a Merkle tree out of the above 3 binary slices.
	rH := merkle.HashFromByteSlices([][]byte{bbeBytes, results.Hash(), ebeBytes})

	// Verify block results.
	if !bytes.Equal(rH, trustedHeader.LastResultsHash) {
		return nil, fmt.Errorf("last results %X does not match with trusted last results %X",
			rH, trustedHeader.LastResultsHash)
	}

	return res, nil
}

func (c *Client) Commit(height *int64) (*ctypes.ResultCommit, error) {
	res, err := c.next.Commit(height)
	if err != nil {
		return nil, err
	}

	// Validate res.
	if err := res.SignedHeader.ValidateBasic(c.lc.ChainID()); err != nil {
		return nil, err
	}
	if res.Height <= 0 {
		return nil, errNegOrZeroHeight
	}

	// Update the light client if we're behind.
	h, err := c.updateLightClientIfNeededTo(res.Height)
	if err != nil {
		return nil, err
	}

	// Verify commit.
	if rH, tH := res.Hash(), h.Hash(); !bytes.Equal(rH, tH) {
		return nil, fmt.Errorf("header %X does not match with trusted header %X",
			rH, tH)
	}

	return res, nil
}

// Tx calls rpcclient#Tx method and then verifies the proof if such was
// requested.
func (c *Client) Tx(hash []byte, prove bool) (*ctypes.ResultTx, error) {
	res, err := c.next.Tx(hash, prove)
	if err != nil || !prove {
		return res, err
	}

	// Validate res.
	if res.Height <= 0 {
		return nil, errNegOrZeroHeight
	}

	// Update the light client if we're behind.
	h, err := c.updateLightClientIfNeededTo(res.Height)
	if err != nil {
		return nil, err
	}

	// Validate the proof.
	return res, res.Proof.Validate(h.DataHash)
}

func (c *Client) TxSearch(query string, prove bool, page, perPage *int, orderBy string) (
	*ctypes.ResultTxSearch, error) {
	return c.next.TxSearch(query, prove, page, perPage, orderBy)
}

// Validators fetches and verifies validators.
//
// WARNING: only full validator sets are verified (when length of validators is
// less than +perPage+. +perPage+ default is 30, max is 100).
func (c *Client) Validators(height *int64, page, perPage *int) (*ctypes.ResultValidators, error) {
	res, err := c.next.Validators(height, page, perPage)
	if err != nil {
		return nil, err
	}

	// Validate res.
	if res.BlockHeight <= 0 {
		return nil, errNegOrZeroHeight
	}

	updateHeight := res.BlockHeight - 1

	// updateHeight can't be zero which happens when we are looking for the validators of the first block
	if updateHeight == 0 {
		updateHeight = 1
	}

	// Update the light client if we're behind.
	h, err := c.updateLightClientIfNeededTo(updateHeight)
	if err != nil {
		return nil, err
	}

	var tH tmbytes.HexBytes
	switch res.BlockHeight {
	case 1:
		// if it's the first block we need to validate with the current validator hash as opposed to the
		// next validator hash
		tH = h.ValidatorsHash
	default:
		tH = h.NextValidatorsHash
	}

	// Verify validators.
	if res.Count <= res.Total {
		if rH := types.NewValidatorSet(res.Validators).Hash(); !bytes.Equal(rH, tH) {
			return nil, fmt.Errorf("validators %X does not match with trusted validators %X",
				rH, tH)
		}
	}

	return res, nil
}

func (c *Client) BroadcastEvidence(ev types.Evidence) (*ctypes.ResultBroadcastEvidence, error) {
	return c.next.BroadcastEvidence(ev)
}

func (c *Client) Subscribe(ctx context.Context, subscriber, query string,
	outCapacity ...int) (out <-chan ctypes.ResultEvent, err error) {
	return c.next.Subscribe(ctx, subscriber, query, outCapacity...)
}

func (c *Client) Unsubscribe(ctx context.Context, subscriber, query string) error {
	return c.next.Unsubscribe(ctx, subscriber, query)
}

func (c *Client) UnsubscribeAll(ctx context.Context, subscriber string) error {
	return c.next.UnsubscribeAll(ctx, subscriber)
}

func (c *Client) updateLightClientIfNeededTo(height int64) (*types.SignedHeader, error) {
	h, err := c.lc.VerifyHeaderAtHeight(height, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to update light client to %d: %w", height, err)
	}
	return h, nil
}

func (c *Client) RegisterOpDecoder(typ string, dec merkle.OpDecoder) {
	c.prt.RegisterOpDecoder(typ, dec)
}

// SubscribeWS subscribes for events using the given query and remote address as
// a subscriber, but does not verify responses (UNSAFE)!
// TODO: verify data
func (c *Client) SubscribeWS(ctx *rpctypes.Context, query string) (*ctypes.ResultSubscribe, error) {
	out, err := c.next.Subscribe(context.Background(), ctx.RemoteAddr(), query)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			select {
			case resultEvent := <-out:
				// We should have a switch here that performs a validation
				// depending on the event's type.
				ctx.WSConn.TryWriteRPCResponse(
					rpctypes.NewRPCSuccessResponse(
						rpctypes.JSONRPCStringID(fmt.Sprintf("%v#event", ctx.JSONReq.ID)),
						resultEvent,
					))
			case <-c.Quit():
				return
			}
		}
	}()

	return &ctypes.ResultSubscribe{}, nil
}

// UnsubscribeWS calls original client's Unsubscribe using remote address as a
// subscriber.
func (c *Client) UnsubscribeWS(ctx *rpctypes.Context, query string) (*ctypes.ResultUnsubscribe, error) {
	err := c.next.Unsubscribe(context.Background(), ctx.RemoteAddr(), query)
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultUnsubscribe{}, nil
}

// UnsubscribeAllWS calls original client's UnsubscribeAll using remote address
// as a subscriber.
func (c *Client) UnsubscribeAllWS(ctx *rpctypes.Context) (*ctypes.ResultUnsubscribe, error) {
	err := c.next.UnsubscribeAll(context.Background(), ctx.RemoteAddr())
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultUnsubscribe{}, nil
}

func parseQueryStorePath(path string) (storeName string, err error) {
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("expected path to start with /")
	}

	paths := strings.SplitN(path[1:], "/", 3)
	switch {
	case len(paths) != 3:
		return "", errors.New("expected format like /store/<storeName>/key")
	case paths[0] != "store":
		return "", errors.New("expected format like /store/<storeName>/key")
	case paths[2] != "key":
		return "", errors.New("expected format like /store/<storeName>/key")
	}

	return paths[1], nil
}