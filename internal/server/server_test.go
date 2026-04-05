package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/rcarson/loom/internal/api"
	"github.com/rcarson/loom/internal/server"
	"github.com/rcarson/loom/internal/store"
	"log/slog"
)

func newTestServer(t *testing.T, token string) (*server.Server, *store.Store) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "loom-server-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	st, err := store.Open(f.Name())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return server.New(st, token, logger), st
}

func do(t *testing.T, srv http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// --- Healthz ---

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "GET", "/healthz", "", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

// --- Auth ---

func TestAuth_Required(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	rr := do(t, srv, "GET", "/api/v1/nodes", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", rr.Code)
	}
}

func TestAuth_ValidToken(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	rr := do(t, srv, "GET", "/api/v1/nodes", "secret", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestAuth_WrongToken(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	rr := do(t, srv, "GET", "/api/v1/nodes", "wrong", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", rr.Code)
	}
}

func TestAuth_NoTokenConfigured(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "GET", "/api/v1/nodes", "", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (no auth configured)", rr.Code)
	}
}

// --- Node registration ---

func TestRegisterNode(t *testing.T) {
	srv, st := newTestServer(t, "")
	rr := do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{
		Name:     "delamain",
		Region:   "home",
		Zone:     "a",
		Tags:     "ssd",
		CPUCores: 8,
		MemoryMB: 16384,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200: %s", rr.Code, rr.Body.String())
	}
	n, err := st.GetNode("delamain")
	if err != nil {
		t.Fatalf("node not stored: %v", err)
	}
	if n.Region != "home" || n.Zone != "a" {
		t.Errorf("node: %+v", n)
	}
}

func TestRegisterNode_MissingName(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Region: "home"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// --- Heartbeat ---

func TestHeartbeat(t *testing.T) {
	srv, st := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})

	before, _ := st.GetNode("n1")
	time.Sleep(10 * time.Millisecond)
	rr := do(t, srv, "PUT", "/api/v1/nodes/n1/heartbeat", "", api.HeartbeatRequest{})
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	after, _ := st.GetNode("n1")
	if !after.LastHeartbeat.After(before.LastHeartbeat) {
		t.Error("heartbeat timestamp not updated")
	}
}

func TestHeartbeat_UnknownNode(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "PUT", "/api/v1/nodes/ghost/heartbeat", "", api.HeartbeatRequest{})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// --- List / Get nodes ---

func TestListNodes(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n2", Region: "home"})

	rr := do(t, srv, "GET", "/api/v1/nodes", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var nodes []api.NodeResponse
	json.NewDecoder(rr.Body).Decode(&nodes)
	if len(nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(nodes))
	}
}

func TestGetNode(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "work", Zone: "b"})

	rr := do(t, srv, "GET", "/api/v1/nodes/n1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var n api.NodeResponse
	json.NewDecoder(rr.Body).Decode(&n)
	if n.Region != "work" || n.Zone != "b" {
		t.Errorf("node: %+v", n)
	}
}

func TestGetNode_NotFound(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "GET", "/api/v1/nodes/ghost", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// --- Job submission ---

const serviceYAML = "name: myapp\ntype: service\nimage: foo:v1\nreplicas: 1\n"

func TestSubmitJob(t *testing.T) {
	srv, _ := newTestServer(t, "")
	// Register a node so scheduling succeeds.
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})

	rr := do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: serviceYAML})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSubmitJob_InvalidSpec(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: "name: myapp\n"}) // missing type
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestSubmitJob_NoNodes(t *testing.T) {
	srv, _ := newTestServer(t, "")
	// No nodes registered — scheduling fails but job is still stored.
	rr := do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: serviceYAML})
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (job stored even without nodes)", rr.Code)
	}
}

// --- List / Get jobs ---

func TestListJobs(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})
	do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: serviceYAML})

	rr := do(t, srv, "GET", "/api/v1/jobs", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var jobs []api.JobResponse
	json.NewDecoder(rr.Body).Decode(&jobs)
	if len(jobs) != 1 || jobs[0].Name != "myapp" {
		t.Errorf("got %+v", jobs)
	}
}

func TestGetJob(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})
	do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: serviceYAML})

	rr := do(t, srv, "GET", "/api/v1/jobs/myapp", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var j api.JobResponse
	json.NewDecoder(rr.Body).Decode(&j)
	if j.Name != "myapp" || j.Type != "service" {
		t.Errorf("job: %+v", j)
	}
	if len(j.Placements) != 1 {
		t.Errorf("placements: got %d want 1", len(j.Placements))
	}
}

func TestGetJob_NotFound(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "GET", "/api/v1/jobs/ghost", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// --- Delete job ---

func TestDeleteJob(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})
	do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: serviceYAML})

	rr := do(t, srv, "DELETE", "/api/v1/jobs/myapp", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	rr2 := do(t, srv, "GET", "/api/v1/jobs/myapp", "", nil)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("job still exists after delete: %d", rr2.Code)
	}
}

func TestDeleteJob_NotFound(t *testing.T) {
	srv, _ := newTestServer(t, "")
	rr := do(t, srv, "DELETE", "/api/v1/jobs/ghost", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// --- Assignments ---

func TestGetAssignments(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})
	do(t, srv, "POST", "/api/v1/jobs", "", api.SubmitJobRequest{SpecYAML: serviceYAML})

	rr := do(t, srv, "GET", "/api/v1/nodes/n1/assignments", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d: %s", rr.Code, rr.Body.String())
	}
	var resp api.AssignmentsResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Assignments) != 1 {
		t.Errorf("assignments: got %d want 1", len(resp.Assignments))
	}
	if resp.Assignments[0].JobName != "myapp" || resp.Assignments[0].Image != "foo:v1" {
		t.Errorf("assignment: %+v", resp.Assignments[0])
	}
}

func TestGetAssignments_NoJobs(t *testing.T) {
	srv, _ := newTestServer(t, "")
	do(t, srv, "POST", "/api/v1/nodes/register", "", api.RegisterNodeRequest{Name: "n1", Region: "home"})

	rr := do(t, srv, "GET", "/api/v1/nodes/n1/assignments", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var resp api.AssignmentsResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Assignments) != 0 {
		t.Errorf("expected 0 assignments, got %d", len(resp.Assignments))
	}
}
