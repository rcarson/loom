package scheduler_test

import (
	"testing"
	"time"

	"github.com/rcarson/loom/internal/api"
	"github.com/rcarson/loom/internal/scheduler"
	"github.com/rcarson/loom/internal/store"
)

func healthy(name, region, zone, tags string) *store.Node {
	return &store.Node{
		Name:          name,
		Region:        region,
		Zone:          zone,
		Tags:          tags,
		Status:        store.NodeStatusHealthy,
		LastHeartbeat: time.Now(),
		RegisteredAt:  time.Now(),
	}
}

func unhealthy(name string) *store.Node {
	return &store.Node{
		Name:          name,
		Status:        store.NodeStatusUnhealthy,
		LastHeartbeat: time.Now(),
		RegisteredAt:  time.Now(),
	}
}

func spec(name string, replicas int, spread api.SpreadStrategy, regions, tags []string) *api.JobSpec {
	return &api.JobSpec{
		Name:     name,
		Type:     api.TypeService,
		Image:    "foo:v1",
		Replicas: replicas,
		Placement: api.PlacementSpec{
			Spread:  spread,
			Regions: regions,
			Tags:    tags,
		},
	}
}

// --- Round-robin (pack) ---

func TestRoundRobin_SingleNode(t *testing.T) {
	nodes := []*store.Node{healthy("node1", "home", "a", "")}
	placements, err := scheduler.Schedule(spec("myapp", 1, api.SpreadPack, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 1 || placements[0].NodeName != "node1" {
		t.Errorf("got %+v", placements)
	}
}

func TestRoundRobin_MultipleReplicas(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", ""),
		healthy("node2", "home", "b", ""),
	}
	placements, err := scheduler.Schedule(spec("myapp", 4, api.SpreadPack, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 4 {
		t.Fatalf("got %d placements, want 4", len(placements))
	}
	// Round-robin: node1, node2, node1, node2
	if placements[0].NodeName != "node1" || placements[1].NodeName != "node2" ||
		placements[2].NodeName != "node1" || placements[3].NodeName != "node2" {
		t.Errorf("not round-robin: %v", nodeNames(placements))
	}
}

func TestRoundRobin_MoreReplicasThanNodes(t *testing.T) {
	nodes := []*store.Node{healthy("node1", "home", "a", "")}
	placements, err := scheduler.Schedule(spec("myapp", 3, api.SpreadPack, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 3 {
		t.Fatalf("got %d placements, want 3", len(placements))
	}
	for _, p := range placements {
		if p.NodeName != "node1" {
			t.Errorf("expected node1, got %q", p.NodeName)
		}
	}
}

// --- Unhealthy node filtering ---

func TestFilter_ExcludesUnhealthyNodes(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", ""),
		unhealthy("node2"),
	}
	placements, err := scheduler.Schedule(spec("myapp", 2, api.SpreadPack, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range placements {
		if p.NodeName == "node2" {
			t.Error("unhealthy node was scheduled")
		}
	}
}

func TestFilter_NoHealthyNodes(t *testing.T) {
	nodes := []*store.Node{unhealthy("node1"), unhealthy("node2")}
	_, err := scheduler.Schedule(spec("myapp", 1, api.SpreadPack, nil, nil), nodes)
	if err == nil {
		t.Fatal("expected error when no healthy nodes")
	}
}

func TestFilter_EmptyNodeList(t *testing.T) {
	_, err := scheduler.Schedule(spec("myapp", 1, api.SpreadPack, nil, nil), nil)
	if err == nil {
		t.Fatal("expected error for empty node list")
	}
}

// --- Region filtering ---

func TestFilter_Region(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", ""),
		healthy("node2", "work", "a", ""),
	}
	placements, err := scheduler.Schedule(spec("myapp", 2, api.SpreadPack, []string{"home"}, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range placements {
		if p.NodeName == "node2" {
			t.Error("node from wrong region was scheduled")
		}
	}
}

func TestFilter_Region_NoMatch(t *testing.T) {
	nodes := []*store.Node{healthy("node1", "home", "a", "")}
	_, err := scheduler.Schedule(spec("myapp", 1, api.SpreadPack, []string{"work"}, nil), nodes)
	if err == nil {
		t.Fatal("expected error when no nodes match region")
	}
}

// --- Tag filtering ---

func TestFilter_Tags(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", "ssd,gpu"),
		healthy("node2", "home", "b", "ssd"),
		healthy("node3", "home", "c", ""),
	}
	placements, err := scheduler.Schedule(spec("myapp", 2, api.SpreadPack, nil, []string{"gpu"}), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range placements {
		if p.NodeName != "node1" {
			t.Errorf("only node1 has gpu tag, got %q", p.NodeName)
		}
	}
}

func TestFilter_Tags_NoMatch(t *testing.T) {
	nodes := []*store.Node{healthy("node1", "home", "a", "ssd")}
	_, err := scheduler.Schedule(spec("myapp", 1, api.SpreadPack, nil, []string{"gpu"}), nodes)
	if err == nil {
		t.Fatal("expected error when no nodes match tags")
	}
}

// --- Spread: region ---

func TestSpreadRegion_OnePerRegion(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", ""),
		healthy("node2", "home", "b", ""),
		healthy("node3", "work", "a", ""),
	}
	placements, err := scheduler.Schedule(spec("myapp", 2, api.SpreadRegion, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(placements))
	}
	regions := map[string]bool{}
	for _, p := range placements {
		for _, n := range nodes {
			if n.Name == p.NodeName {
				regions[n.Region] = true
			}
		}
	}
	if !regions["home"] || !regions["work"] {
		t.Errorf("spread region did not cover both regions: %v", placements)
	}
}

func TestSpreadRegion_MoreReplicasThanRegions(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", ""),
		healthy("node2", "work", "a", ""),
	}
	// 3 replicas, 2 regions — should fill remaining with round-robin
	placements, err := scheduler.Schedule(spec("myapp", 3, api.SpreadRegion, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 3 {
		t.Fatalf("got %d placements, want 3", len(placements))
	}
}

// --- Spread: zone ---

func TestSpreadZone_OnePerZone(t *testing.T) {
	nodes := []*store.Node{
		healthy("node1", "home", "a", ""),
		healthy("node2", "home", "b", ""),
		healthy("node3", "home", "a", ""), // same zone as node1
	}
	placements, err := scheduler.Schedule(spec("myapp", 2, api.SpreadZone, nil, nil), nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(placements))
	}
	zones := map[string]bool{}
	for _, p := range placements {
		for _, n := range nodes {
			if n.Name == p.NodeName {
				zones[n.Zone] = true
			}
		}
	}
	if !zones["a"] || !zones["b"] {
		t.Errorf("spread zone did not cover both zones: %v", placements)
	}
}

// --- Job name propagation ---

func TestPlacement_JobNameSet(t *testing.T) {
	nodes := []*store.Node{healthy("node1", "home", "a", "")}
	placements, _ := scheduler.Schedule(spec("myjob", 1, api.SpreadPack, nil, nil), nodes)
	if placements[0].JobName != "myjob" {
		t.Errorf("job name not set: got %q", placements[0].JobName)
	}
}

func nodeNames(placements []scheduler.Placement) []string {
	names := make([]string, len(placements))
	for i, p := range placements {
		names[i] = p.NodeName
	}
	return names
}
