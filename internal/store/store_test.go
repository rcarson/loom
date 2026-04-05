package store_test

import (
	"os"
	"testing"
	"time"

	"github.com/rcarson/loom/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "loom-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	s, err := store.Open(f.Name())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- Node tests ---

func TestUpsertAndGetNode(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	n := &store.Node{
		Name:          "delamain",
		Region:        "home",
		Zone:          "a",
		Tags:          "ssd,gpu",
		CPUCores:      8,
		MemoryMB:      16384,
		Status:        store.NodeStatusHealthy,
		LastHeartbeat: now,
		RegisteredAt:  now,
	}
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	got, err := s.GetNode("delamain")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if got.Name != n.Name || got.Region != n.Region || got.Zone != n.Zone {
		t.Errorf("got %+v, want name=%s region=%s zone=%s", got, n.Name, n.Region, n.Zone)
	}
	if got.CPUCores != 8 || got.MemoryMB != 16384 {
		t.Errorf("resources: got cpu=%d mem=%d", got.CPUCores, got.MemoryMB)
	}
}

func TestUpsertNode_UpdatesExisting(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	n := &store.Node{Name: "node1", Region: "home", Zone: "a", Status: store.NodeStatusHealthy, LastHeartbeat: now, RegisteredAt: now}
	s.UpsertNode(n)

	n.Region = "work"
	n.Status = store.NodeStatusUnhealthy
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ := s.GetNode("node1")
	if got.Region != "work" {
		t.Errorf("region not updated: got %q", got.Region)
	}
}

func TestListNodes(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	for _, name := range []string{"node-b", "node-a", "node-c"} {
		s.UpsertNode(&store.Node{Name: name, Status: store.NodeStatusHealthy, LastHeartbeat: now, RegisteredAt: now})
	}
	nodes, err := s.ListNodes()
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	if nodes[0].Name != "node-a" {
		t.Errorf("not sorted: first is %q", nodes[0].Name)
	}
}

func TestGetNode_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetNode("missing")
	if err == nil {
		t.Fatal("expected error for missing node")
	}
}

func TestUpdateNodeHeartbeat(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	s.UpsertNode(&store.Node{Name: "n1", Status: store.NodeStatusHealthy, LastHeartbeat: now, RegisteredAt: now})

	later := now.Add(15 * time.Second)
	if err := s.UpdateNodeHeartbeat("n1", store.NodeStatusHealthy, later); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, _ := s.GetNode("n1")
	if !got.LastHeartbeat.Equal(later) {
		t.Errorf("heartbeat not updated: got %v want %v", got.LastHeartbeat, later)
	}
}

func TestUpdateNodeHeartbeat_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateNodeHeartbeat("ghost", store.NodeStatusHealthy, time.Now())
	if err == nil {
		t.Fatal("expected error for missing node")
	}
}

func TestMarkStaleNodesUnhealthy(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().UTC().Add(-10 * time.Minute)
	recent := time.Now().UTC()
	s.UpsertNode(&store.Node{Name: "stale", Status: store.NodeStatusHealthy, LastHeartbeat: old, RegisteredAt: old})
	s.UpsertNode(&store.Node{Name: "fresh", Status: store.NodeStatusHealthy, LastHeartbeat: recent, RegisteredAt: recent})

	cutoff := time.Now().UTC().Add(-1 * time.Minute)
	if err := s.MarkStaleNodesUnhealthy(cutoff); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	stale, _ := s.GetNode("stale")
	fresh, _ := s.GetNode("fresh")
	if stale.Status != store.NodeStatusUnhealthy {
		t.Errorf("stale node status: got %q want unhealthy", stale.Status)
	}
	if fresh.Status != store.NodeStatusHealthy {
		t.Errorf("fresh node status: got %q want healthy", fresh.Status)
	}
}

// --- Job tests ---

func TestUpsertAndGetJob(t *testing.T) {
	s := newTestStore(t)
	j := &store.Job{Name: "myapp", Type: "service", SpecYAML: "name: myapp\ntype: service\n"}
	if err := s.UpsertJob(j); err != nil {
		t.Fatalf("upsert job: %v", err)
	}
	got, err := s.GetJob("myapp")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Name != "myapp" || got.Type != "service" {
		t.Errorf("got %+v", got)
	}
}

func TestUpsertJob_UpdatesSpec(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: "v1"})
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: "v2"})
	got, _ := s.GetJob("myapp")
	if got.SpecYAML != "v2" {
		t.Errorf("spec not updated: got %q", got.SpecYAML)
	}
}

func TestListJobs(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"job-b", "job-a"} {
		s.UpsertJob(&store.Job{Name: name, Type: "service", SpecYAML: ""})
	}
	jobs, err := s.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].Name != "job-a" {
		t.Errorf("list not sorted: %v", jobs)
	}
}

func TestDeleteJob(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "todelete", Type: "service", SpecYAML: ""})
	if err := s.DeleteJob("todelete"); err != nil {
		t.Fatalf("delete job: %v", err)
	}
	_, err := s.GetJob("todelete")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

// --- Placement tests ---

func TestCreateAndListPlacements(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: ""})
	p := &store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusPending}
	if err := s.CreatePlacement(p); err != nil {
		t.Fatalf("create placement: %v", err)
	}
	if p.ID == 0 {
		t.Error("placement ID not set after insert")
	}
	placements, err := s.ListPlacementsForJob("myapp")
	if err != nil {
		t.Fatalf("list placements: %v", err)
	}
	if len(placements) != 1 || placements[0].NodeName != "node1" {
		t.Errorf("got %+v", placements)
	}
}

func TestUpdatePlacementStatus(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: ""})
	p := &store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusPending}
	s.CreatePlacement(p)
	if err := s.UpdatePlacementStatus(p.ID, store.PlacementStatusRunning, "abc123"); err != nil {
		t.Fatalf("update placement: %v", err)
	}
	list, _ := s.ListPlacementsForJob("myapp")
	if list[0].Status != store.PlacementStatusRunning || list[0].ContainerID != "abc123" {
		t.Errorf("status not updated: %+v", list[0])
	}
}

func TestListPlacementsForNode(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: ""})
	s.CreatePlacement(&store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusRunning})
	s.CreatePlacement(&store.Placement{JobName: "myapp", NodeName: "node2", Status: store.PlacementStatusRunning})
	// stopped placements are excluded
	p := &store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusStopped}
	s.CreatePlacement(p)

	list, err := s.ListPlacementsForNode("node1")
	if err != nil {
		t.Fatalf("list placements for node: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("got %d placements, want 1 (stopped excluded)", len(list))
	}
}

func TestDeletePlacementsForNode(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: ""})
	s.CreatePlacement(&store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusRunning})
	s.CreatePlacement(&store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusRunning})
	if err := s.DeletePlacementsForNode("node1"); err != nil {
		t.Fatalf("delete placements: %v", err)
	}
	list, _ := s.ListPlacementsForNode("node1")
	if len(list) != 0 {
		t.Errorf("expected 0 placements after delete, got %d", len(list))
	}
}

func TestDeleteJob_CascadesPlacements(t *testing.T) {
	s := newTestStore(t)
	s.UpsertJob(&store.Job{Name: "myapp", Type: "service", SpecYAML: ""})
	s.CreatePlacement(&store.Placement{JobName: "myapp", NodeName: "node1", Status: store.PlacementStatusRunning})
	s.DeleteJob("myapp")
	list, _ := s.ListPlacementsForJob("myapp")
	if len(list) != 0 {
		t.Errorf("placements not cascaded on job delete: got %d", len(list))
	}
}

// --- Event tests ---

func TestAppendAndListEvents(t *testing.T) {
	s := newTestStore(t)
	for _, msg := range []string{"first", "second", "third"} {
		s.AppendEvent(&store.Event{Type: "deploy", JobName: "myapp", NodeName: "node1", Message: msg})
	}
	events, err := s.ListEvents(10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	// newest first
	if events[0].Message != "third" {
		t.Errorf("not newest first: got %q", events[0].Message)
	}
}

func TestListEvents_Limit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		s.AppendEvent(&store.Event{Type: "test", Message: "msg"})
	}
	events, _ := s.ListEvents(3)
	if len(events) != 3 {
		t.Errorf("limit not applied: got %d", len(events))
	}
}
