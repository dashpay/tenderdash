package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

const (
	flagFromMnemonic       = "from-mnemonic"
	flagDerivationPath     = "derivation-path"
	defaultDeriviationPath = "m/9'/5'/3'/4'/0'"
)

var (
	useSeedPhrase  bool
	derivationPath string
)

// MakeGenNodeKeyCommand creates command that allows the generation of a node key.
// It prints JSON-encoded NodeKey to the standard output.
func MakeGenNodeKeyCommand(*config.Config, log.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-node-key",
		Short: "Generate a new node key for this node and print its ID",
		Long: `Generate a new node key for this node and print its ID. 
			Note that the key is not saved to disk.

			Node key can be generated randomly (default) or derived from BIP39 mnemonic phrase.
			Seed phrase and optional password is read from standard input.
`,
		RunE: genNodeKey,
	}

	cmd.Flags().BoolVar(&useSeedPhrase, flagFromMnemonic, false,
		"derive key from BIP39 seed mnemonic phrase (read from stdin)")
	cmd.Flags().StringVar(&derivationPath, flagDerivationPath, defaultDeriviationPath,
		"BIP32 derivation path")
	return cmd
}

func readMnemonic(in io.Reader, out io.Writer) (mnemonic string, password string, err error) {
	reader := bufio.NewReader(in)

	_, _ = out.Write([]byte("Enter BIP39 mnemonic: "))
	mnemonic, err = reader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("cannot read mnemonic: %w", err)
	}

	_, _ = out.Write([]byte("Enter passphrase (can be empty): "))
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		var bytePassword []byte
		bytePassword, err = term.ReadPassword(int(f.Fd()))
		password = string(bytePassword)
	} else {
		password, err = reader.ReadString('\n')
	}
	_, _ = out.Write([]byte{'\n'})
	if err != nil {
		return "", "", fmt.Errorf("cannot read passphrase: %w", err)
	}

	return strings.TrimSpace(mnemonic), strings.TrimSpace(password), nil
}

func nodeKeyFromMnemonic(cmd *cobra.Command, args []string) (types.NodeKey, error) {
	mnemonic, password, err := readMnemonic(cmd.InOrStdin(), cmd.OutOrStdout())
	if err != nil {
		return types.NodeKey{}, err
	}
	privKey, err := ed25519.FromBip39Mnemonic(mnemonic, password, derivationPath)
	if err != nil {
		return types.NodeKey{}, err
	}

	return types.NodeKey{
		ID:      types.NodeIDFromPubKey(privKey.PubKey()),
		PrivKey: privKey,
	}, nil
}

func genNodeKey(cmd *cobra.Command, args []string) error {
	var (
		nodeKey types.NodeKey
		err     error
	)
	if useSeedPhrase {
		nodeKey, err = nodeKeyFromMnemonic(cmd, args)
		if err != nil {
			return fmt.Errorf("cannot generate key from mnemonic: %w", err)
		}
	} else {
		if derivationPath != "" {
			return fmt.Errorf("--%s can be used only with --%s", flagDerivationPath, flagFromMnemonic)
		}

		nodeKey = types.GenNodeKey()
	}

	bz, err := json.Marshal(nodeKey)
	if err != nil {
		return fmt.Errorf("nodeKey -> json: %w", err)
	}

	fmt.Printf(`%v
`, string(bz))

	return nil
}
