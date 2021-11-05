// TODO move somewhere
package logparser

import (
	"fmt"

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
}

// NewGraph initializes graph visualization mechanism
func NewGraph(logger log.Logger) (Graph, error) {
	var err error
	graph := Graph{
		logger:   logger,
		graphViz: graphviz.New(),
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
func (g *Graph) getOrAddEdge(label string, src, dst *cgraph.Node) (*cgraph.Edge, error) {
	edge, err := g.graph.CreateEdge(label, src, dst)
	if err != nil {
		return nil, err
	}
	edge.SetDir(cgraph.ForwardDir)
	edge.SetLabel(label)
	return edge, nil
}

// Add adds nodes `src` and `dst`, and connects them with an edge with label `label`
func (g *Graph) Add(src, dst, label string) error {
	srcNode, err := g.getOrAddNode(src)
	if err != nil {
		return err
	}
	dstNode, err := g.getOrAddNode(dst)
	if err != nil {
		return err
	}
	_, err = g.getOrAddEdge(label, srcNode, dstNode)
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
