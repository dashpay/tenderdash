// TODO move somewhere
package logparser

import (
	"fmt"
	"time"

	"github.com/goccy/go-graphviz"
	"github.com/goccy/go-graphviz/cgraph"
	"github.com/tendermint/tendermint/libs/log"
)

const (
	// FormatPNG represents PNG format of output
	FormatPNG = "png"
	// FormatSVG represents SVG format of output
	FormatSVG = "svg"
)

// Graph does all the hard work required to visualize vote flow within the network
type Graph struct {
	graphViz *graphviz.Graphviz
	graph    *cgraph.Graph
	logger   log.Logger
	edges    map[string]edgeInfo
}

type edgeInfo struct {
	*cgraph.Edge
	timestamp time.Time
}

// NewGraph initializes graph visualization mechanism
func NewGraph(logger log.Logger) (Graph, error) {
	var err error
	graph := Graph{
		logger:   logger,
		graphViz: graphviz.New(),
		edges:    make(map[string]edgeInfo),
	}

	if graph.graph, err = graph.graphViz.Graph(); err != nil {
		return graph, err
	}
	graph.graph.SetConcentrate(false)
	return graph, nil
}

// getOrAddNode creates a node if it does not exist yet
func (g *Graph) getOrAddNode(proTxHash string) (*cgraph.Node, error) {
	node, err := g.graph.Node(proTxHash)
	if err != nil {
		return nil, err
	}
	if node == nil {
		node, err = g.graph.CreateNode(proTxHash)
	}
	return node, err
}

// getOrAddNode creates an edge if it does not exist yet
func (g *Graph) getOrAddEdge(label string, src, dst *cgraph.Node, t time.Time) (*cgraph.Edge, error) {
	edgeName := src.Name() + "->" + dst.Name()
	if edge, ok := g.edges[edgeName]; ok {
		oldTime := edge.timestamp

		if oldTime.Before(t) {
			g.logger.Debug("ignoring duplicate edge",
				"src", src.Name(), "dst", dst.Name(), "label", label,
				"oldTime", oldTime, "newTime", t)
			return edge.Edge, nil
		}

		g.logger.Debug("overwriting duplicate edge",
			"src", src.Name(), "dst", dst.Name(), "label", label,
			"oldTime", oldTime, "newTime", t)
	}

	edge, err := g.graph.CreateEdge(label, src, dst)
	if err != nil {
		return nil, err
	}
	edge.SetDir(cgraph.ForwardDir)
	edge.SetLabel(label)

	g.edges[edgeName] = edgeInfo{
		Edge:      edge,
		timestamp: t,
	}

	return edge, nil
}

// Add adds nodes `src` and `dst`, and connects them with an edge with label `label`
func (g *Graph) Add(src, dst, label string, eventTime time.Time) error {
	srcNode, err := g.getOrAddNode(src)
	if err != nil {
		return err
	}
	dstNode, err := g.getOrAddNode(dst)
	if err != nil {
		return err
	}
	_, err = g.getOrAddEdge(label, srcNode, dstNode, eventTime)
	if err != nil {
		return err
	}

	return nil
}

// Save renders the graph and saves it to a file
func (g *Graph) Save(filePath string, format string) error {
	var imgFormat graphviz.Format
	switch format {
	case FormatPNG:
		imgFormat = graphviz.PNG
	case FormatSVG:
		imgFormat = graphviz.SVG
	default:
		return fmt.Errorf("invalid format %s", format)
	}

	return g.graphViz.RenderFilename(g.graph, imgFormat, filePath)
}

// Stats returns some statistics about the graph: number of nodes, number of edges
func (g *Graph) Stats() string {
	nodes := g.graph.NumberNodes()
	edges := g.graph.NumberEdges()
	return fmt.Sprintf("nodes: %d, edges: %d", nodes, edges)
}

// ColorNode colors the provided node
func (g *Graph) ColorNode(name string) error {
	n, err := g.graph.Node(name)
	if err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("node %s not found", name)
	}
	n.SetStyle(cgraph.FilledNodeStyle)
	n.SetColor("darkgreen")
	n.SetFillColor("green")
	return nil
}
