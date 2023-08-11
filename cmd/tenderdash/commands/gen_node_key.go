package commands

import (
	"bufio"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/term"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

const (
	flagFromMnemonic       = "from-mnemonic"
	flagFromPem            = "from-pem-file"
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
		PreRunE: genNodeKeyFlagsPreRunE,
		RunE:    genNodeKeyRunE,
	}

	cmd.Flags().BoolVar(&useSeedPhrase, flagFromMnemonic, false,
		"derive key from BIP39 seed mnemonic phrase (read from stdin)")
	cmd.Flags().StringVar(&pemFile, flagFromPem, "",
		"read PEM-encoded ED25519 private key file (use '-' for stdin)")
	cmd.Flags().StringVar(&derivationPath, flagDerivationPath, defaultDeriviationPath,
		"BIP32 derivation path")
	return cmd
}

func genNodeKeyFlagsPreRunE(cmd *cobra.Command, args []string) error {
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

func genNodeKeyRunE(cmd *cobra.Command, args []string) error {
	var (
		nodeKey types.NodeKey
		err     error
	)
	switch {
	case useSeedPhrase:
		if nodeKey, err = nodeKeyFromMnemonic(cmd, args); err != nil {
			return fmt.Errorf("cannot process mnemonic: %w", err)
		}

	case pemFile != "":
		in, err := stdinOrFile(cmd, pemFile)
		if err != nil {
			return fmt.Errorf("cannot open file %s: %w", pemFile, err)
		}
		defer in.Close()

		if nodeKey, err = nodeKeyFromPem(in); err != nil {
			return fmt.Errorf("cannot process PEM file %s: %w", pemFile, err)
		}

	default:
		nodeKey = types.GenNodeKey()
	}

	bz, err := json.Marshal(nodeKey)
	if err != nil {
		return fmt.Errorf("cannot format node key: %w", err)
	}

	cmd.Println(string(bz))
	return nil
}

// readMnemonic reads mnemonic and passphrase from `in`.
// If `in` is a terminal, it reads the passphrase without displaying it.
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

// nodeKeyFromMnemonic reads BIP39 mnemonic and optional passphrase from stdin, and derives node key from it.
func nodeKeyFromMnemonic(cmd *cobra.Command, args []string) (types.NodeKey, error) {
	mnemonic, password, err := readMnemonic(cmd.InOrStdin(), cmd.OutOrStdout())
	if err != nil {
		return types.NodeKey{}, err
	}
	// Validate the mnemonic
	if _, err := bip39.EntropyFromMnemonic(mnemonic); err != nil {
		if errors.Is(err, bip39.ErrInvalidMnemonic) {
			return types.NodeKey{}, fmt.Errorf("mnemonic must have 12, 15, 18, 21 or 24 words")
		}
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

// nodeKeyFromPem reads PEM ED25519 private key from `in` and formats it as a `types.NodeKey`
func nodeKeyFromPem(in io.Reader) (nodeKey types.NodeKey, err error) {
	pemData, err := io.ReadAll(in)
	if err != nil {
		return nodeKey, err
	}

	// ignore `rest`, as we don't support multiple items in one PEM file
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nodeKey, fmt.Errorf("cannot PEM-decode input file")
	}

	privKey, err := ed25519.FromDER(block.Bytes)
	if err != nil {
		return nodeKey, err
	}

	return types.NodeKey{
		ID:      types.NodeIDFromPubKey(privKey.PubKey()),
		PrivKey: privKey,
	}, nil
}

// stdinOrFile returns input stream pointing to stdin if filename is `-`, or to the `filename` opened in read mode
func stdinOrFile(cmd *cobra.Command, filename string) (io.ReadCloser, error) {
	if filename == "-" {
		return io.NopCloser(cmd.InOrStdin()), nil
	}
	return os.Open(filename)
}
