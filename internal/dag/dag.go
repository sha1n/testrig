package dag

import "fmt"

// Node represents a node in a directed graph.
type Node interface {
	ID() string
	Dependencies() []string
}

// UnknownDepError reports a dependency edge to a node not present in the graph.
// Validate returns this error so callers can rewrap it with their domain
// vocabulary (e.g. "service" instead of "node").
type UnknownDepError struct {
	Node       string
	MissingDep string
}

func (e *UnknownDepError) Error() string {
	return fmt.Sprintf("%s depends on unknown node %s", e.Node, e.MissingDep)
}

// Validate checks the graph is well-formed: every dependency points to a
// known node and there are no cycles.
func Validate(nodes []Node) error {
	nodeMap := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID()] = n
	}

	for _, n := range nodes {
		for _, depID := range n.Dependencies() {
			if _, ok := nodeMap[depID]; !ok {
				return &UnknownDepError{Node: n.ID(), MissingDep: depID}
			}
		}
	}

	visited := make(map[string]bool, len(nodes))
	recStack := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if hasCycle(n.ID(), nodeMap, visited, recStack) {
			return fmt.Errorf("circular dependency detected involving: %s", n.ID())
		}
	}
	return nil
}

func hasCycle(id string, nodeMap map[string]Node, visited, recStack map[string]bool) bool {
	if recStack[id] {
		return true
	}
	if visited[id] {
		return false
	}

	visited[id] = true
	recStack[id] = true

	node, ok := nodeMap[id]
	if ok {
		for _, depID := range node.Dependencies() {
			if hasCycle(depID, nodeMap, visited, recStack) {
				return true
			}
		}
	}

	recStack[id] = false
	return false
}
