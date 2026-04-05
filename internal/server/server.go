package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rcarson/loom/internal/api"
	"github.com/rcarson/loom/internal/config"
	"github.com/rcarson/loom/internal/scheduler"
	"github.com/rcarson/loom/internal/store"
)

const reconcileInterval = 15 * time.Second
const heartbeatTimeout = 45 * time.Second // 3 missed heartbeats at 15s

// Server is the loom-server HTTP API and reconciliation loop.
type Server struct {
	store  *store.Store
	token  string
	logger *slog.Logger
	mux    *http.ServeMux
}

// New creates a new Server.
func New(st *store.Store, token string, logger *slog.Logger) *Server {
	s := &Server{
		store:  st,
		token:  token,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /api/v1/nodes/register", s.auth(s.handleRegisterNode))
	s.mux.HandleFunc("PUT /api/v1/nodes/{name}/heartbeat", s.auth(s.handleHeartbeat))
	s.mux.HandleFunc("GET /api/v1/nodes", s.auth(s.handleListNodes))
	s.mux.HandleFunc("GET /api/v1/nodes/{name}", s.auth(s.handleGetNode))
	s.mux.HandleFunc("POST /api/v1/jobs", s.auth(s.handleSubmitJob))
	s.mux.HandleFunc("GET /api/v1/jobs", s.auth(s.handleListJobs))
	s.mux.HandleFunc("GET /api/v1/jobs/{name}", s.auth(s.handleGetJob))
	s.mux.HandleFunc("DELETE /api/v1/jobs/{name}", s.auth(s.handleDeleteJob))
	s.mux.HandleFunc("GET /api/v1/nodes/{name}/assignments", s.auth(s.handleGetAssignments))
}

// Run starts the HTTP server and reconciliation loop, blocking until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	httpServer := &http.Server{
		Addr:    addr,
		Handler: s,
	}

	go s.reconcileLoop(ctx)

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutCtx)
	}()

	s.logger.Info("loom-server listening", "addr", addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// reconcileLoop runs the background reconciliation on a timer.
func (s *Server) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcile()
		}
	}
}

// reconcile marks stale nodes unhealthy and reschedules their placements.
func (s *Server) reconcile() {
	cutoff := time.Now().UTC().Add(-heartbeatTimeout)
	if err := s.store.MarkStaleNodesUnhealthy(cutoff); err != nil {
		s.logger.Error("reconcile: mark stale nodes", "err", err)
		return
	}

	// Find unhealthy nodes that still have active placements.
	nodes, err := s.store.ListNodes()
	if err != nil {
		s.logger.Error("reconcile: list nodes", "err", err)
		return
	}
	for _, n := range nodes {
		if n.Status != store.NodeStatusUnhealthy {
			continue
		}
		placements, err := s.store.ListPlacementsForNode(n.Name)
		if err != nil || len(placements) == 0 {
			continue
		}
		s.logger.Warn("reconcile: rescheduling placements from unhealthy node", "node", n.Name, "count", len(placements))
		if err := s.store.DeletePlacementsForNode(n.Name); err != nil {
			s.logger.Error("reconcile: delete placements", "node", n.Name, "err", err)
			continue
		}
		// Reschedule each affected job.
		affected := map[string]bool{}
		for _, p := range placements {
			affected[p.JobName] = true
		}
		for jobName := range affected {
			if err := s.scheduleJob(jobName); err != nil {
				s.logger.Error("reconcile: reschedule job", "job", jobName, "err", err)
			}
		}
	}
}

// scheduleJob runs the scheduler for a job and persists the resulting placements.
func (s *Server) scheduleJob(jobName string) error {
	job, err := s.store.GetJob(jobName)
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}
	spec, err := config.ParseJobSpec([]byte(job.SpecYAML))
	if err != nil {
		return fmt.Errorf("parse spec: %w", err)
	}
	nodes, err := s.store.ListNodes()
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	placements, err := scheduler.Schedule(spec, nodes)
	if err != nil {
		return err
	}
	for _, p := range placements {
		if err := s.store.CreatePlacement(&store.Placement{
			JobName:  p.JobName,
			NodeName: p.NodeName,
			Status:   store.PlacementStatusPending,
		}); err != nil {
			return fmt.Errorf("create placement: %w", err)
		}
	}
	s.store.AppendEvent(&store.Event{
		Type:    "scheduled",
		JobName: jobName,
		Message: fmt.Sprintf("%d placements created", len(placements)),
	})
	return nil
}

// --- Handlers ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	var req api.RegisterNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	now := time.Now().UTC()
	n := &store.Node{
		Name:          req.Name,
		Region:        req.Region,
		Zone:          req.Zone,
		Tags:          req.Tags,
		CPUCores:      req.CPUCores,
		MemoryMB:      req.MemoryMB,
		Status:        store.NodeStatusHealthy,
		LastHeartbeat: now,
		RegisteredAt:  now,
	}
	if err := s.store.UpsertNode(n); err != nil {
		s.logger.Error("register node", "node", req.Name, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to register node")
		return
	}
	s.logger.Info("node registered", "node", req.Name, "region", req.Region, "zone", req.Zone)
	s.store.AppendEvent(&store.Event{Type: "node_registered", NodeName: req.Name})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.UpdateNodeHeartbeat(name, store.NodeStatusHealthy, time.Now().UTC()); err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list nodes")
		return
	}
	resp := make([]api.NodeResponse, len(nodes))
	for i, n := range nodes {
		resp[i] = nodeToResponse(n)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	n, err := s.store.GetNode(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, nodeToResponse(n))
}

func (s *Server) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	var req api.SubmitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	spec, err := config.ParseJobSpec([]byte(req.SpecYAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	job := &store.Job{Name: spec.Name, Type: string(spec.Type), SpecYAML: req.SpecYAML}
	if err := s.store.UpsertJob(job); err != nil {
		s.logger.Error("submit job: upsert", "job", spec.Name, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to store job")
		return
	}
	// Clear existing placements before rescheduling.
	// UpsertJob already stored the spec; now delete only the placements.
	jobs, _ := s.store.ListPlacementsForJob(spec.Name)
	for _, p := range jobs {
		s.store.UpdatePlacementStatus(p.ID, store.PlacementStatusStopped, p.ContainerID)
	}
	if err := s.scheduleJob(spec.Name); err != nil {
		s.logger.Warn("submit job: schedule", "job", spec.Name, "err", err)
		// Not fatal — job is stored, scheduling may succeed once nodes register.
	}
	s.logger.Info("job submitted", "job", spec.Name, "type", spec.Type)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "job": spec.Name})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	resp := make([]api.JobResponse, 0, len(jobs))
	for _, j := range jobs {
		jr, err := s.buildJobResponse(j)
		if err != nil {
			continue
		}
		resp = append(resp, jr)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	j, err := s.store.GetJob(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	jr, err := s.buildJobResponse(j)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build job response")
		return
	}
	writeJSON(w, http.StatusOK, jr)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.store.GetJob(name); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err := s.store.DeleteJob(name); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete job")
		return
	}
	s.store.AppendEvent(&store.Event{Type: "job_deleted", JobName: name})
	s.logger.Info("job deleted", "job", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetAssignments(w http.ResponseWriter, r *http.Request) {
	nodeName := r.PathValue("name")
	placements, err := s.store.ListPlacementsForNode(nodeName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list placements")
		return
	}

	assignments := make([]api.Assignment, 0, len(placements))
	for _, p := range placements {
		job, err := s.store.GetJob(p.JobName)
		if err != nil {
			continue
		}
		spec, err := config.ParseJobSpec([]byte(job.SpecYAML))
		if err != nil {
			continue
		}
		assignments = append(assignments, api.Assignment{
			PlacementID: p.ID,
			JobName:     p.JobName,
			Image:       spec.Image,
			Port:        spec.Port,
			Command:     spec.Command,
			Env:         envMapToSlice(spec.Env),
		})
	}
	writeJSON(w, http.StatusOK, api.AssignmentsResponse{Assignments: assignments})
}

// --- auth middleware ---

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		bearer := r.Header.Get("Authorization")
		if !strings.HasPrefix(bearer, "Bearer ") || strings.TrimPrefix(bearer, "Bearer ") != s.token {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// --- helpers ---

func (s *Server) buildJobResponse(j *store.Job) (api.JobResponse, error) {
	placements, err := s.store.ListPlacementsForJob(j.Name)
	if err != nil {
		return api.JobResponse{}, err
	}
	prs := make([]api.PlacementResponse, len(placements))
	for i, p := range placements {
		prs[i] = api.PlacementResponse{
			ID:          p.ID,
			NodeName:    p.NodeName,
			ContainerID: p.ContainerID,
			Status:      string(p.Status),
			UpdatedAt:   p.UpdatedAt,
		}
	}
	return api.JobResponse{
		Name:       j.Name,
		Type:       j.Type,
		Placements: prs,
		CreatedAt:  j.CreatedAt,
		UpdatedAt:  j.UpdatedAt,
	}, nil
}

func nodeToResponse(n *store.Node) api.NodeResponse {
	return api.NodeResponse{
		Name:          n.Name,
		Region:        n.Region,
		Zone:          n.Zone,
		Tags:          n.Tags,
		CPUCores:      n.CPUCores,
		MemoryMB:      n.MemoryMB,
		Status:        string(n.Status),
		LastHeartbeat: n.LastHeartbeat,
		RegisteredAt:  n.RegisteredAt,
	}
}

func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg})
}
