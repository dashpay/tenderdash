package core

import (
	"context"
	"fmt"
	"time"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
)

type RetryClient struct {
	next    Client
	backoff time.Duration // initial delay for first retry
	retries int           // number of retries
	logger  log.Logger
}

func NewRetryClient(
	next Client,
	backoff time.Duration,
	retries int,
	logger log.Logger,
) *RetryClient {
	if backoff <= 0 {
		logger.Debug("dash core backoff is 0, setting to 1s")
		backoff = time.Second // default backoff
	}
	if retries <= 0 {
		logger.Debug("dash core retries counter is 0, setting to 1")
		retries = 1 // one retry by default
	}
	return &RetryClient{
		next:    next,
		backoff: backoff,
		retries: retries,
		logger:  logger.With("module", "rpcclient"),
	}
}

// retry runs some code with retries and a timeout.
// It implements exponential backoff.
func retry[T any](ctx context.Context, sc *RetryClient, fn func() (T, error)) (T, error) {
	var val T
	backoff := sc.backoff
	var err error

	for i := 0; i < sc.retries; i++ {
		val, err = fn()
		if err == nil {
			return val, nil
		}

		select {
		case <-ctx.Done():
			return val, ctx.Err()
		case <-time.After(backoff):
			// Exponential backoff with a cap (e.g., 10x initial timeout)
			if backoff < sc.backoff*10 {
				backoff *= 2
				if backoff > sc.backoff*10 {
					backoff = sc.backoff * 10
				}
			}
			sc.logger.Trace("dash core rpc operation failed, retrying",
				"err", err,
				"attempt", i+1, "max_attempts", sc.retries,
				"backoff", backoff,
			)
		}
	}
	return val, fmt.Errorf("exhausted %d retry attempts: %w", sc.retries, err)
}

// Ensure RetryClient implements Client interface
var _ Client = (*RetryClient)(nil)

// Close closes the underlying connection
func (rc *RetryClient) Close() error {
	return rc.next.Close()
}

// Ping sends a ping request to the remote node
func (rc *RetryClient) Ping() error {
	_, err := retry(context.Background(), rc, func() (struct{}, error) {
		return struct{}{}, rc.next.Ping()
	})
	return err
}

func (rc *RetryClient) QuorumInfo(
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) (*btcjson.QuorumInfoResult, error) {
	return retry(context.Background(), rc, func() (*btcjson.QuorumInfoResult, error) {
		return rc.next.QuorumInfo(quorumType, quorumHash)
	})
}

func (rc *RetryClient) MasternodeStatus() (*btcjson.MasternodeStatusResult, error) {
	return retry(context.Background(), rc, func() (*btcjson.MasternodeStatusResult, error) {
		return rc.next.MasternodeStatus()
	})
}

func (rc *RetryClient) GetNetworkInfo() (*btcjson.GetNetworkInfoResult, error) {
	return retry(context.Background(), rc, func() (*btcjson.GetNetworkInfoResult, error) {
		return rc.next.GetNetworkInfo()
	})
}

func (rc *RetryClient) MasternodeListJSON(filter string) (map[string]btcjson.MasternodelistResultJSON, error) {
	return retry(context.Background(), rc, func() (map[string]btcjson.MasternodelistResultJSON, error) {
		return rc.next.MasternodeListJSON(filter)
	})
}

func (rc *RetryClient) QuorumSign(
	quorumType btcjson.LLMQType,
	requestID bytes.HexBytes,
	messageHash bytes.HexBytes,
	quorumHash crypto.QuorumHash,
) (*btcjson.QuorumSignResult, error) {
	return retry(context.Background(), rc, func() (*btcjson.QuorumSignResult, error) {
		return rc.next.QuorumSign(quorumType, requestID, messageHash, quorumHash)
	})
}

func (rc *RetryClient) QuorumVerify(
	quorumType btcjson.LLMQType,
	requestID bytes.HexBytes,
	messageHash bytes.HexBytes,
	signature bytes.HexBytes,
	quorumHash crypto.QuorumHash,
) (bool, error) {
	return retry(context.Background(), rc, func() (bool, error) {
		return rc.next.QuorumVerify(quorumType, requestID, messageHash, signature, quorumHash)
	})
}
