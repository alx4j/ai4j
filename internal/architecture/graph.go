// Package architecture contains executable dependency fitness functions.
package architecture

import (
	"fmt"
	"sort"
	"strings"
)

const modulePrefix = "github.com/alx4j/ai4j/"

type Graph map[string][]string

type Violation struct {
	Package string
	Import  string
	Rule    string
}

func (v Violation) Error() string {
	if v.Import == "" {
		return fmt.Sprintf("%s violates %s", v.Package, v.Rule)
	}
	return fmt.Sprintf("%s imports %s, violating %s", v.Package, v.Import, v.Rule)
}

func Check(graph Graph) []Violation {
	var violations []Violation
	for pkg, imports := range graph {
		for _, dependency := range imports {
			if forbiddenImport(pkg, dependency) {
				violations = append(violations, Violation{Package: pkg, Import: dependency, Rule: "target-neutral dependency direction"})
			}
		}
	}
	for _, cycle := range cycles(graph) {
		violations = append(violations, Violation{Package: strings.Join(cycle, " -> "), Rule: "acyclic package graph"})
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].Error() < violations[j].Error() })
	return violations
}

func forbiddenImport(pkg, dependency string) bool {
	if !strings.HasPrefix(dependency, modulePrefix) {
		return false
	}
	switch {
	case under(pkg, modulePrefix+"internal/domain"), under(pkg, modulePrefix+"internal/fault"),
		under(pkg, modulePrefix+"internal/pathsafe"):
		return true
	case under(pkg, modulePrefix+"internal/lifecycle"):
		return strings.HasPrefix(dependency, modulePrefix+"internal/registry") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/testkit") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/target/") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/host/") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/source/")
	case under(pkg, modulePrefix+"internal/registry"):
		return strings.HasPrefix(dependency, modulePrefix+"internal/target/") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/host/") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/source/")
	case under(pkg, modulePrefix+"internal/host/darwin"):
		return under(dependency, modulePrefix+"internal/environment") ||
			under(dependency, modulePrefix+"internal/registry") ||
			under(dependency, modulePrefix+"internal/app") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/target/") ||
			strings.HasPrefix(dependency, modulePrefix+"internal/source/")
	default:
		return false
	}
}

func under(pkg, root string) bool { return pkg == root || strings.HasPrefix(pkg, root+"/") }

func cycles(graph Graph) [][]string {
	const (
		unseen = iota
		visiting
		done
	)
	state := make(map[string]int, len(graph))
	stack := make([]string, 0, len(graph))
	var found [][]string
	var visit func(string)
	visit = func(node string) {
		state[node] = visiting
		stack = append(stack, node)
		for _, dependency := range graph[node] {
			if _, exists := graph[dependency]; !exists {
				continue
			}
			switch state[dependency] {
			case unseen:
				visit(dependency)
			case visiting:
				start := 0
				for stack[start] != dependency {
					start++
				}
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, dependency)
				found = append(found, cycle)
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = done
	}
	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if state[node] == unseen {
			visit(node)
		}
	}
	return found
}
