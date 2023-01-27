package commands

import (
	"bufio"
	"encoding/json"
	"encoding/pem"
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
	flagFromPem            = "from-pem"
	flagDerivationPath     = "derivation-path"
	defaultDeriviationPath = "m/9'/5'/3'/4'/0'"
)

var (
	useSeedPhrase  bool
	pemFile        string
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
Seed phrase and optional password is read from standard input.`,
		PreRunE: verifyGenNodeKeyFlags,
		RunE:    genNodeKey,
	}

	cmd.Flags().BoolVar(&useSeedPhrase, flagFromMnemonic, false,
		"derive key from BIP39 seed mnemonic phrase (read from stdin)")
	cmd.Flags().StringVar(&pemFile, flagFromPem, "",
		"read PEM-encoded ED25519 private key file (use '-' for stdin)")
	cmd.Flags().StringVar(&derivationPath, flagDerivationPath, defaultDeriviationPath,
		"BIP32 derivation path")
	return cmd
}

func verifyGenNodeKeyFlags(cmd *cobra.Command, args []string) error {
	if useSeedPhrase && pemFile != "" {
		return fmt.Errorf("--%s cannot be be used with --%s", flagFromMnemonic, flagFromPem)
	}

	if !useSeedPhrase && derivationPath != "" && derivationPath != defaultDeriviationPath {
		return fmt.Errorf("--%s can be used only with --%s", flagDerivationPath, flagFromMnemonic)
	}

	if pemFile != "" && pemFile != "-" {
		if _, err := os.Stat(pemFile); err != nil {
			return fmt.Errorf("--%s: cannot load %s: %w", flagFromPem, pemFile, err)
		}
	}

	return nil
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
func nodeKeyFromPem(in io.Reader) (nodeKey types.NodeKey, err error) {
	var pemData []byte

	if pemData, err = io.ReadAll(in); err != nil {
		return nodeKey, err
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nodeKey, fmt.Errorf("cannot PEM-decode input file")
	}
	// x509.MarshalPKCS8PrivateKey(priv)

	privKey, err := ed25519.FromDER(block.Bytes)
	if err != nil {
		return nodeKey, fmt.Errorf("cannot parse private key: %w", err)
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
	switch {
	case useSeedPhrase:
		nodeKey, err = nodeKeyFromMnemonic(cmd, args)

	case pemFile != "":

		if pemFile == "-" {
			nodeKey, err = nodeKeyFromPem(cmd.InOrStdin())
		} else {
			var in io.ReadCloser
			if in, err = os.Open(pemFile); err == nil {
				defer in.Close()
				nodeKey, err = nodeKeyFromPem(in)
			}
		}

	default:
		nodeKey = types.GenNodeKey()
	}

	if err != nil {
		return fmt.Errorf("cannot generate node key: %w", err)
	}

	bz, err := json.Marshal(nodeKey)
	if err != nil {
		return fmt.Errorf("nodeKey -> json: %w", err)
	}

	cmd.Printf("%v\n", string(bz))
	return nil
}
