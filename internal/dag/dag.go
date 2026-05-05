package dag

import "fmt"

// Node represents a node in a directed graph.
type Node interface {
	ID() string
	Dependencies() []string
}

// Validate checks for circular dependencies in a set of nodes.
func Validate(nodes []Node) error {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	nodeMap := make(map[string]Node)

	for _, n := range nodes {
		nodeMap[n.ID()] = n
	}

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
