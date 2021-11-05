package logparser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/rand"
)

// LogProcessor does all the hard work required to parse logs and visualize vote flow within the network
type LogProcessor struct {
	decoder  *json.Decoder
	graph    Graph
	logger   log.Logger
	parsers  []Parser
	logCount int64
	hitCount int64
	errCount int64
}

// NewLogProcessor creates a new log processor
func NewLogProcessor(input io.Reader, logger log.Logger, graph Graph) (LogProcessor, error) {
	parser := LogProcessor{
		decoder: json.NewDecoder(input),
		logger:  logger,
		graph:   graph,
	}
	return parser, nil
}

// AddParser adds new log parser that will interpret the log as it arrives
func (g *LogProcessor) AddParser(p Parser) {
	g.parsers = append(g.parsers, p)
}

// Run is the main processing logic that reads data from input, parses it and adds to graph
func (g *LogProcessor) Run(ctx context.Context) error {
	for {
		item := logItem{}
		if err := g.decoder.Decode(&item); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if rand.Intn(10000000) == 1 {
				g.logger.Debug("error parsing item", "error", err)
			}
			continue // we ignore errors
		}

		g.logCount++
		for _, p := range g.parsers {
			if p.Check(item) {
				src, dst, label, err := p.Parse(item)
				if err != nil {
					g.logger.Error("error parsing item", "error", err)
					g.errCount++
					continue // non-fatal, try another parser
				}

				if err := g.graph.Add(src, dst, label); err != nil {
					g.logger.Error("error adding item to the graph", "error", err)
					return err // fatal
				}
				g.hitCount++
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("execution expired")
			default:
			}
		}
	}
}

// Stats return some interesting statistics about log processing
func (g *LogProcessor) Stats() string {
	return fmt.Sprintf("processed: %d, hits: %d, errors: %d", g.logCount, g.hitCount, g.errCount)
}
