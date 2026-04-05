package scheduler

import (
	"fmt"

	"github.com/rcarson/loom/internal/api"
	"github.com/rcarson/loom/internal/store"
)

// Placement is a decision to run a job on a specific node.
type Placement struct {
	JobName  string
	NodeName string
}

// Schedule returns a list of placement decisions for the given job spec across the available nodes.
// Only healthy nodes are considered. Returns an error if no healthy nodes match the constraints.
func Schedule(spec *api.JobSpec, nodes []*store.Node) ([]Placement, error) {
	candidates := filter(spec, nodes)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("scheduler: no healthy nodes match constraints for job %q", spec.Name)
	}

	return place(spec, candidates), nil
}

// filter returns nodes that are healthy and satisfy the placement constraints.
func filter(spec *api.JobSpec, nodes []*store.Node) []*store.Node {
	var out []*store.Node
	for _, n := range nodes {
		if n.Status != store.NodeStatusHealthy {
			continue
		}
		if !matchesRegions(n, spec.Placement.Regions) {
			continue
		}
		if !matchesTags(n, spec.Placement.Tags) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// place distributes replicas across candidates according to the spread strategy.
func place(spec *api.JobSpec, candidates []*store.Node) []Placement {
	switch spec.Placement.Spread {
	case api.SpreadRegion:
		return placeByAttribute(spec, candidates, func(n *store.Node) string { return n.Region })
	case api.SpreadZone:
		return placeByAttribute(spec, candidates, func(n *store.Node) string { return n.Zone })
	default: // SpreadPack or empty — round-robin
		return roundRobin(spec.Name, candidates, spec.Replicas)
	}
}

// placeByAttribute picks one candidate per unique attribute value (region or zone),
// up to spec.Replicas total placements.
func placeByAttribute(spec *api.JobSpec, candidates []*store.Node, attr func(*store.Node) string) []Placement {
	seen := map[string]*store.Node{}
	for _, n := range candidates {
		key := attr(n)
		if _, exists := seen[key]; !exists {
			seen[key] = n
		}
	}

	var placements []Placement
	for _, n := range seen {
		if len(placements) >= spec.Replicas {
			break
		}
		placements = append(placements, Placement{JobName: spec.Name, NodeName: n.Name})
	}

	// If spread left us short of replicas (e.g. more replicas than regions),
	// fill remaining slots with round-robin across all candidates.
	remaining := spec.Replicas - len(placements)
	if remaining > 0 {
		// Collect nodes not already used.
		used := map[string]bool{}
		for _, p := range placements {
			used[p.NodeName] = true
		}
		var unused []*store.Node
		for _, n := range candidates {
			if !used[n.Name] {
				unused = append(unused, n)
			}
		}
		placements = append(placements, roundRobin(spec.Name, unused, remaining)...)
		// If still short (fewer nodes than remaining), wrap around all candidates.
		if len(placements) < spec.Replicas {
			placements = append(placements, roundRobin(spec.Name, candidates, spec.Replicas-len(placements))...)
		}
	}

	return placements
}

// roundRobin assigns n placements by cycling through candidates.
func roundRobin(jobName string, candidates []*store.Node, n int) []Placement {
	if len(candidates) == 0 || n == 0 {
		return nil
	}
	placements := make([]Placement, n)
	for i := range placements {
		placements[i] = Placement{
			JobName:  jobName,
			NodeName: candidates[i%len(candidates)].Name,
		}
	}
	return placements
}

// matchesRegions returns true if the node's region is in the allowed list,
// or if no regions are specified (all regions allowed).
func matchesRegions(n *store.Node, regions []string) bool {
	if len(regions) == 0 {
		return true
	}
	for _, r := range regions {
		if n.Region == r {
			return true
		}
	}
	return false
}

// matchesTags returns true if the node has all required tags,
// or if no tags are required.
func matchesTags(n *store.Node, required []string) bool {
	if len(required) == 0 {
		return true
	}
	nodeTags := splitTags(n.Tags)
	for _, req := range required {
		if !contains(nodeTags, req) {
			return false
		}
	}
	return true
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			t := s[start:i]
			if t != "" {
				tags = append(tags, t)
			}
			start = i + 1
		}
	}
	return tags
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
