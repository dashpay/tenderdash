package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/jsontypes"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
	tmos "github.com/dashpay/tenderdash/libs/os"
	"github.com/dashpay/tenderdash/privval"
	tmgrpc "github.com/dashpay/tenderdash/privval/grpc"
)

// MakeShowValidatorCommand constructs a command to show the validator info.
func MakeShowValidatorCommand(conf *config.Config, logger log.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "show-validator",
		Short: "Show this node's validator info",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var (
				pubKey crypto.PubKey
				err    error
				bctx   = cmd.Context()
			)
			//TODO: remove once gRPC is the only supported protocol
			protocol, _ := tmnet.ProtocolAndAddress(conf.PrivValidator.ListenAddr)
			switch protocol {
			case "grpc":
				pvsc, err := tmgrpc.DialRemoteSigner(
					bctx,
					conf.PrivValidator,
					conf.ChainID(),
					logger,
					conf.Instrumentation.Prometheus,
				)
				if err != nil {
					return fmt.Errorf("can't connect to remote validator %w", err)
				}

				ctx, cancel := context.WithTimeout(bctx, ctxTimeout)
				defer cancel()

				_, err = pvsc.GetProTxHash(ctx)
				if err != nil {
					return fmt.Errorf("can't get proTxHash: %w", err)
				}
			default:

				keyFilePath := conf.PrivValidator.KeyFile()
				if !tmos.FileExists(keyFilePath) {
					return fmt.Errorf("private validator file %s does not exist", keyFilePath)
				}

				pv, err := privval.LoadFilePV(keyFilePath, conf.PrivValidator.StateFile())
				if err != nil {
					return err
				}

				ctx, cancel := context.WithTimeout(bctx, ctxTimeout)
				defer cancel()

				_, err = pv.GetProTxHash(ctx)
				if err != nil {
					return fmt.Errorf("can't get proTxHash: %w", err)
				}
			}

			bz, err := jsontypes.Marshal(pubKey)
			if err != nil {
				return fmt.Errorf("failed to marshal private validator pubkey: %w", err)
			}

			fmt.Println(string(bz))
			return nil
		},
	}

}
