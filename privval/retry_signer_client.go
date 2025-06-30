package privval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// RetryableSignerClient is a signer client that can retry operations
type RetryableSignerClient interface {
	io.Closer
	types.PrivValidator

	Ping(ctx context.Context) error
}

// RetrySignerClient wraps SignerClient adding retry for each operation (except
// Ping) w/ a timeout.
type RetrySignerClient struct {
	next    RetryableSignerClient
	retries int
	timeout time.Duration
}

// NewRetrySignerClient returns RetrySignerClient. If +retries+ is 0, the
// client will be retrying each operation indefinitely.
func NewRetrySignerClient(_ctx context.Context, sc RetryableSignerClient, retries int, timeout time.Duration) *RetrySignerClient {
	return &RetrySignerClient{sc, retries, timeout}
}

var _ types.PrivValidator = (*RetrySignerClient)(nil)

func (sc *RetrySignerClient) Close() error {
	return sc.next.Close()
}

//--------------------------------------------------------
// Implement PrivValidator

func (sc *RetrySignerClient) Ping(ctx context.Context) error {
	return sc.next.Ping(ctx)
}

func (sc *RetrySignerClient) ExtractIntoValidator(ctx context.Context, quorumHash crypto.QuorumHash) *types.Validator {
	pubKey, _ := sc.GetPubKey(ctx, quorumHash)
	proTxHash, _ := sc.GetProTxHash(ctx)
	if len(proTxHash) != crypto.DefaultHashSize {
		panic("proTxHash wrong length")
	}
	return &types.Validator{
		PubKey:      pubKey,
		VotingPower: types.DefaultDashVotingPower,
		ProTxHash:   proTxHash,
	}
}

// retry runs some code with retries and a timeout.
// It implements exponential backoff.
func retry[T any](ctx context.Context, sc *RetrySignerClient, fn func() (T, error)) (T, error) {
	var zero T
	backoff := sc.timeout
	for i := 0; i < sc.retries || sc.retries == 0; i++ {
		val, err := fn()
		if err == nil {
			return val, nil
		}
		// If remote signer errors, we don't retry.
		if _, ok := err.(*RemoteSignerError); ok {
			return zero, err
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
			// Exponential backoff with a cap (e.g., 10x initial timeout)
			if backoff < sc.timeout*10 {
				backoff *= 2
				if backoff > sc.timeout*10 {
					backoff = sc.timeout * 10
				}
			}
		}
	}
	return zero, fmt.Errorf("exhausted all attempts: %w", ctx.Err())
}

func (sc *RetrySignerClient) GetPubKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	return retry(ctx, sc, func() (crypto.PubKey, error) {
		return sc.next.GetPubKey(ctx, quorumHash)
	})
}

func (sc *RetrySignerClient) GetProTxHash(ctx context.Context) (crypto.ProTxHash, error) {
	return retry(ctx, sc, func() (crypto.ProTxHash, error) {
		proTxHash, err := sc.next.GetProTxHash(ctx)
		if len(proTxHash) != crypto.ProTxHashSize {
			return nil, fmt.Errorf("retrySignerClient proTxHash is invalid size")
		}
		return proTxHash, err
	})
}

func (sc *RetrySignerClient) GetFirstQuorumHash(_ctx context.Context) (crypto.QuorumHash, error) {
	return nil, errors.New("getFirstQuorumHash should not be called on a signer client")
}

func (sc *RetrySignerClient) GetThresholdPublicKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	return retry(ctx, sc, func() (crypto.PubKey, error) {
		return sc.next.GetThresholdPublicKey(ctx, quorumHash)
	})
}

func (sc *RetrySignerClient) GetHeight(ctx context.Context, quorumHash crypto.QuorumHash) (int64, error) {
	return retry(ctx, sc, func() (int64, error) {
		return sc.next.GetHeight(ctx, quorumHash)
	})
}

func (sc *RetrySignerClient) SignVote(
	ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash,
	vote *tmproto.Vote, _logger log.Logger) error {
	_, err := retry(ctx, sc, func() (struct{}, error) {
		return struct{}{}, sc.next.SignVote(ctx, chainID, quorumType, quorumHash, vote, nil)
	})
	return err
}

func (sc *RetrySignerClient) SignProposal(
	ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash, proposal *tmproto.Proposal,
) (tmbytes.HexBytes, error) {
	return retry(ctx, sc, func() (tmbytes.HexBytes, error) {
		return sc.next.SignProposal(ctx, chainID, quorumType, quorumHash, proposal)
	})
}

func (sc *RetrySignerClient) UpdatePrivateKey(
	ctx context.Context, privateKey crypto.PrivKey, quorumHash crypto.QuorumHash, thresholdPublicKey crypto.PubKey, height int64,
) {
	// not retryable
	sc.next.UpdatePrivateKey(ctx, privateKey, quorumHash, thresholdPublicKey, height)
}

func (sc *RetrySignerClient) GetPrivateKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PrivKey, error) {
	return retry(ctx, sc, func() (crypto.PrivKey, error) {
		return sc.next.GetPrivateKey(ctx, quorumHash)
	})
}
