//go:generate ../../scripts/mockery_generate.sh Client

package core

import (
	"fmt"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	rpc "github.com/dashpay/dashd-go/rpcclient"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
)

const ModuleName = "rpcclient"

var sleep = time.Sleep

// QuorumVerifier represents subset of priv validator features that
// allows verification of threshold signatures.
type QuorumVerifier interface {
	// QuorumVerify verifies quorum signature
	QuorumVerify(
		quorumType btcjson.LLMQType,
		requestID bytes.HexBytes,
		messageHash bytes.HexBytes,
		signature bytes.HexBytes,
		quorumHash bytes.HexBytes,
	) (bool, error)
}

type Client interface {
	QuorumVerifier

	// QuorumInfo returns quorum info
	QuorumInfo(quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash) (*btcjson.QuorumInfoResult, error)
	// MasternodeStatus returns masternode status
	MasternodeStatus() (*btcjson.MasternodeStatusResult, error)
	// GetNetworkInfo returns network info
	GetNetworkInfo() (*btcjson.GetNetworkInfoResult, error)
	// MasternodeListJSON returns masternode list json
	MasternodeListJSON(filter string) (map[string]btcjson.MasternodelistResultJSON, error)
	// QuorumSign signs message in a quorum session
	QuorumSign(
		quorumType btcjson.LLMQType,
		requestID bytes.HexBytes,
		messageHash bytes.HexBytes,
		quorumHash bytes.HexBytes,
	) (*btcjson.QuorumSignResult, error)
	QuorumVerify(
		quorumType btcjson.LLMQType,
		requestID bytes.HexBytes,
		messageHash bytes.HexBytes,
		signature bytes.HexBytes,
		quorumHash bytes.HexBytes,
	) (bool, error)
	// Close Closes connection to dashd
	Close() error
	// Ping Sends ping to dashd
	Ping() error
}

// RPCClient implements Client
// Handles connection to the underlying dashd instance
type RPCClient struct {
	endpoint *rpc.Client
	logger   log.Logger
}

// NewRPCClient returns an instance of Client.
// it will start the endpoint (if not already started)
func NewRPCClient(host string, username string, password string, logger log.Logger) (*RPCClient, error) {
	if host == "" {
		return nil, fmt.Errorf("unable to establish connection to the Dash Core node")
	}

	// Connect to local dash core RPC server using HTTP POST mode.
	connCfg := &rpc.ConnConfig{
		Host:         host,
		User:         username,
		Pass:         password,
		HTTPPostMode: true, // Dash core only supports HTTP POST mode
		DisableTLS:   true, // Dash core does not provide TLS by default
	}
	// Notice the notification parameter is nil since notifications are
	// not supported in HTTP POST mode.
	client, err := rpc.New(connCfg, nil)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		return nil, fmt.Errorf("logger must be set")
	}

	dashCoreClient := RPCClient{
		endpoint: client,
		logger:   logger,
	}

	return &dashCoreClient, nil
}

// Close closes the underlying connection
func (rpcClient *RPCClient) Close() error {
	rpcClient.endpoint.Shutdown()
	return nil
}

// Ping sends a ping request to the remote signer
func (rpcClient *RPCClient) Ping() error {
	err := rpcClient.endpoint.Ping()
	rpcClient.logger.Trace("core rpc call Ping", "error", err)
	if err != nil {
		return err
	}

	return nil
}

func (rpcClient *RPCClient) QuorumInfo(
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) (*btcjson.QuorumInfoResult, error) {
	resp, err := rpcClient.endpoint.QuorumInfo(quorumType, quorumHash.String(), false)
	rpcClient.logger.Trace("core rpc call QuorumInfo",
		"quorumType", quorumType,
		"quorumHash", quorumHash,
		"includeSkShare", false,
		"resp", resp,
		"error", err,
	)
	return resp, err
}

func (rpcClient *RPCClient) MasternodeStatus() (*btcjson.MasternodeStatusResult, error) {
	resp, err := rpcClient.endpoint.MasternodeStatus()
	rpcClient.logger.Trace("core rpc call MasternodeStatus",
		"resp", resp,
		"error", err)
	return resp, err
}

func (rpcClient *RPCClient) GetNetworkInfo() (*btcjson.GetNetworkInfoResult, error) {
	resp, err := rpcClient.endpoint.GetNetworkInfo()
	rpcClient.logger.Trace("core rpc call GetNetworkInfo",
		"resp", resp,
		"error", err)
	return resp, err
}

func (rpcClient *RPCClient) MasternodeListJSON(filter string) (
	map[string]btcjson.MasternodelistResultJSON,
	error,
) {
	resp, err := rpcClient.endpoint.MasternodeListJSON(filter)
	rpcClient.logger.Trace("core rpc call MasternodeListJSON",
		"filter", filter,
		"resp", resp,
		"error", err)
	return resp, err
}

func (rpcClient *RPCClient) QuorumSign(
	quorumType btcjson.LLMQType,
	requestID bytes.HexBytes,
	messageHash bytes.HexBytes,
	quorumHash crypto.QuorumHash,
) (*btcjson.QuorumSignResult, error) {
	if err := quorumType.Validate(); err != nil {
		return nil, err
	}
	quorumSignResultWithBool, err := rpcClient.endpoint.QuorumPlatformSign(
		requestID.String(),
		messageHash.String(),
		quorumHash.String(),
		false,
	)

	rpcClient.logger.Trace("core rpc call QuorumSign",
		"quorumType", quorumType,
		"requestID", requestID.String(),
		"messageHash", messageHash.String(),
		"quorumHash", quorumHash.String(),
		"submit", false,
		"resp", quorumSignResultWithBool,
		"error", err,
	)

	if quorumSignResultWithBool == nil {
		return nil, err
	}

	// as QuorumPlatformSign does not provide the quorum type, we need to check it manually
	// to ensure we deliver what was requested by the caller
	quorumSignResult := quorumSignResultWithBool.QuorumSignResult
	if quorumType != btcjson.LLMQType(quorumSignResult.LLMQType) {
		return nil, fmt.Errorf("possible misconfiguration: quorum platform sign uses unexpected quorum type %d, expected %d",
			quorumSignResultWithBool.LLMQType, quorumType)
	}

	return &quorumSignResult, err
}

func (rpcClient *RPCClient) QuorumVerify(
	quorumType btcjson.LLMQType,
	requestID bytes.HexBytes,
	messageHash bytes.HexBytes,
	signature bytes.HexBytes,
	quorumHash crypto.QuorumHash,
) (bool, error) {
	if err := quorumType.Validate(); err != nil {
		return false, err
	}
	resp, err := rpcClient.endpoint.QuorumVerify(
		quorumType,
		requestID.String(),
		messageHash.String(),
		signature.String(),
		quorumHash.String(),
	)
	rpcClient.logger.Trace("core rpc call QuorumVerify",
		"quorumType", quorumType,
		"requestID", requestID.String(),
		"messageHash", messageHash.String(),
		"signature", signature.String(),
		"quorumHash", quorumHash.String(),
		"resp", resp,
		"error", err,
	)
	return resp, err

}

// WaitForMNReady waits until the masternode is ready
func WaitForMNReady(client Client, retryTimeout time.Duration) error {
	for {
		result, err := client.MasternodeStatus()
		if err != nil {
			return fmt.Errorf("failed to get masternode status: %w", err)
		}
		switch result.State {
		case btcjson.MNStatusStateReady, btcjson.MNStatusStatePoseBanned:
			return nil
		case btcjson.MNStatusStateWaitingForProtx:
			sleep(retryTimeout)
		case btcjson.MNStatusStateRemoved,
			btcjson.MNStatusStateOperatorKeyChanged,
			btcjson.MNStatusStateProtxIpChanged,
			btcjson.MNStatusStateError,
			btcjson.MNStatusStateUnknown:
			return fmt.Errorf("unexpected masternode state %s", result.State)
		}
	}
}
