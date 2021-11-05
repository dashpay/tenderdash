package logparser

// Parser represents chunk of code that parse logs to get info required for rendering of a graph
type Parser interface {
	// Check checks if the parser can parse provided log item
	Check(logItem) bool
	// Parse parses log item and retreieves src and dst node, and label of the edge between them
	Parse(item logItem) (src string, dst string, label string, err error)
}
