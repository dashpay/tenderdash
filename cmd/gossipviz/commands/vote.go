package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tendermint/tendermint/cmd/gossipviz/logparser"
	"github.com/tendermint/tendermint/libs/log"
)

// VoteCmd generatas main cobra command for the Vote object
func VoteCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "vote",
		Short: "Visualize votes gossip",
	}

	cmd.AddCommand((&voteGraphCommand{}).command())
	return &cmd
}

type voteGraphCommand struct {
	inputFile    string
	outputFile   string
	outputFormat string
	filter       logparser.Filter
}

func (v *voteGraphCommand) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "graph",
		PreRunE: v.preRunE,
		RunE:    v.runE,
	}
	v.addFlags(cmd)
	return cmd
}

func (v *voteGraphCommand) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&v.inputFile, "in", "", "Input file to use instead of stdin")
	cmd.Flags().StringVar(&v.outputFile, "out", "", "Output file to save rendered graph; required")
	cmd.Flags().StringVarP(&v.outputFormat, "format", "f", "", "Output format to use, one of: svg (default), png")
	cmd.Flags().Int64Var(&v.filter.Height, "height", -1, "Limit graph to provided height")
	cmd.Flags().Int64Var(&v.filter.Round, "round", -1, "Limit graph to provided round")
	cmd.Flags().StringVar(&v.filter.AuthorProTxHash, "author", "", "Filter by vote author proTxHash")

}

func (v *voteGraphCommand) preRunE(cmd *cobra.Command, args []string) error {
	return v.validateParams()
}

func (v *voteGraphCommand) validateParams() error {
	if v.inputFile != "" {
		if _, err := os.Stat(v.inputFile); err != nil {
			return fmt.Errorf("invalid input file %s: %w", v.inputFile, err)
		}
	}

	if v.outputFile == "" {
		return fmt.Errorf("please provide output file with --out")
	}

	if v.outputFormat == "" {
		v.outputFormat = logparser.FormatSVG
	}
	if v.outputFormat != logparser.FormatSVG && v.outputFormat != logparser.FormatPNG {
		return fmt.Errorf("invalid format %s, use one of: %s, %s", v.outputFormat, logparser.FormatPNG, logparser.FormatSVG)
	}

	return nil
}

func (v *voteGraphCommand) runE(cmd *cobra.Command, args []string) error {
	logger := log.NewTMLogger(os.Stderr)
	input := os.Stdin
	if v.inputFile != "" {
		f, err := os.Open(v.inputFile)
		if err != nil {
			return fmt.Errorf("cannot open file %s: %w", v.inputFile, err)
		}
		defer f.Close()
		input = f
	}

	logger.Info("Processing vote logs",
		"input", v.inputFile,
		"filters", v.filter,
	)

	graph, err := logparser.NewGraph(logger)
	if err != nil {
		return err
	}

	logProcessor, err := logparser.NewLogProcessor(input, logger, graph)
	if err != nil {
		return err
	}
	logProcessor.AddParser(logparser.NewVoteParser(v.filter))
	ctx, cancel := context.WithTimeout(context.TODO(), 60*time.Second)
	defer cancel()
	if err := logProcessor.Run(ctx); err != nil {
		return err
	}

	if v.filter.AuthorProTxHash != "" {
		if err := graph.ColorNode(v.filter.AuthorProTxHash); err != nil {
			logger.Debug("cannot set author node color", "node", v.filter.AuthorProTxHash, "error", err)
		}
	}

	logger.Info("Rendering image", "output", v.outputFile, "outputFormat", v.outputFormat)
	if err := graph.Save(v.outputFile, v.outputFormat); err != nil {
		return err
	}

	logger.Info("Done", "logStats", logProcessor.Stats(), "graphStats", graph.Stats())

	return nil
}
