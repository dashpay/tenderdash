package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/jsontypes"
	"github.com/dashpay/tenderdash/libs/log"
	tmos "github.com/dashpay/tenderdash/libs/os"
	"github.com/dashpay/tenderdash/privval"
)

// MakeShowValidatorCommand constructs a command to show the validator info.
func MakeShowValidatorCommand(conf *config.Config, _ log.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "show-validator",
		Short: "Show this node's validator info",
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				pubKey crypto.PubKey
				err    error
				bctx   = cmd.Context()
			)
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

			bz, err := jsontypes.Marshal(pubKey)
			if err != nil {
				return fmt.Errorf("failed to marshal private validator pubkey: %w", err)
			}

			fmt.Println(string(bz))
			return nil
		},
	}

}
