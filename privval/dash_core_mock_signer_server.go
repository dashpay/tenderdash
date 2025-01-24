package privval

import (
	"github.com/go-pkgz/jrpc"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

type DashCoreMockSignerServer struct {
	server     *jrpc.Server
	chainID    string
	quorumHash crypto.QuorumHash
	privVal    types.PrivValidator

	// handlerMtx tmsync.Mutex
}

func NewDashCoreMockSignerServer(
	_endpoint *SignerDialerEndpoint,
	chainID string,
	quorumHash crypto.QuorumHash,
	privVal types.PrivValidator,
) *DashCoreMockSignerServer {
	// create plugin (jrpc server)
	jrpcServer := jrpc.NewServer("/command",
		jrpc.Auth("user", "password"),
		jrpc.WithSignature("dashcoremock", "Dash Core Group", version.ABCIVersion),
	)

	mockServer := &DashCoreMockSignerServer{
		server:     jrpcServer,
		chainID:    chainID,
		quorumHash: quorumHash,
		privVal:    privVal,
	}

	return mockServer
}

// OnStart implements service.Service.
func (ss *DashCoreMockSignerServer) Run(port int) error {
	return ss.server.Run(port)
}
