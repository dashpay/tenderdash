package privval

import (
	"github.com/go-pkgz/jrpc"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/types"
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
	mockServer := &DashCoreMockSignerServer{
		server: &jrpc.Server{
			API:        "/command",     // base url for rpc calls
			AuthUser:   "user",         // basic auth user name
			AuthPasswd: "password",     // basic auth password
			AppName:    "dashcoremock", // plugin name for headers
		},
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
