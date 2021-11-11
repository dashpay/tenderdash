package e2e

type nodeQueryFunc func(*Node) bool

func (t Testnet) findNodes(query nodeQueryFunc) []*Node {
	var out []*Node
	for _, n := range t.Nodes {
		if query(n) {
			out = append(out, n)
		}
	}
	return out
}

func eqMode(mode Mode) nodeQueryFunc {
	return func(n *Node) bool {
		return n.Mode == mode
	}
}

func eqName(name string) nodeQueryFunc {
	return func(n *Node) bool {
		return n.Name == name
	}
}

func not(query nodeQueryFunc) nodeQueryFunc {
	return func(n *Node) bool {
		return !query(n)
	}
}

func orQuery(fns ...nodeQueryFunc) nodeQueryFunc {
	return func(node *Node) bool {
		for _, fn := range fns {
			if fn(node) {
				return true
			}
		}
		return false
	}
}

func eqNames(names []string) []nodeQueryFunc {
	query := make([]nodeQueryFunc, len(names))
	for i, name := range names {
		query[i] = eqName(name)
	}
	return query
}
