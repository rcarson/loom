package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/rcarson/loom/internal/api"
)

const (
	heartbeatInterval = 15 * time.Second
	pollInterval      = 15 * time.Second
	containerPrefix   = "loom-"
)

// Config holds the loom-agent configuration.
type Config struct {
	ServerURL string
	Token     string
	NodeName  string
	Region    string
	Zone      string
	Tags      string // comma-separated
	CPUCores  int
	MemoryMB  int
}

// Agent is the loom-agent: registers with the server, polls for assignments, and manages containers.
type Agent struct {
	cfg    Config
	docker *client.Client
	http   *http.Client
	logger *slog.Logger
}

// New creates a new Agent. The Docker client connects to the local daemon.
func New(cfg Config, logger *slog.Logger) (*Agent, error) {
	dc, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("agent: docker client: %w", err)
	}
	return &Agent{
		cfg:    cfg,
		docker: dc,
		http:   &http.Client{Timeout: 10 * time.Second},
		logger: logger,
	}, nil
}

// Run registers the agent, then runs the heartbeat and assignment-poll loops until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("agent: register: %w", err)
	}
	a.logger.Info("agent registered", "node", a.cfg.NodeName, "server", a.cfg.ServerURL)

	heartbeat := time.NewTicker(heartbeatInterval)
	poll := time.NewTicker(pollInterval)
	defer heartbeat.Stop()
	defer poll.Stop()

	// Initial poll immediately.
	a.reconcileContainers(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if err := a.sendHeartbeat(ctx); err != nil {
				a.logger.Warn("heartbeat failed", "err", err)
			}
		case <-poll.C:
			a.reconcileContainers(ctx)
		}
	}
}

// register sends the node registration request to the server.
func (a *Agent) register(ctx context.Context) error {
	req := api.RegisterNodeRequest{
		Name:     a.cfg.NodeName,
		Region:   a.cfg.Region,
		Zone:     a.cfg.Zone,
		Tags:     a.cfg.Tags,
		CPUCores: a.cfg.CPUCores,
		MemoryMB: a.cfg.MemoryMB,
	}
	return a.post(ctx, "/api/v1/nodes/register", req, nil)
}

// sendHeartbeat notifies the server the agent is alive.
func (a *Agent) sendHeartbeat(ctx context.Context) error {
	return a.put(ctx, "/api/v1/nodes/"+a.cfg.NodeName+"/heartbeat", api.HeartbeatRequest{}, nil)
}

// reconcileContainers fetches assignments and ensures the right containers are running.
func (a *Agent) reconcileContainers(ctx context.Context) {
	var resp api.AssignmentsResponse
	if err := a.get(ctx, "/api/v1/nodes/"+a.cfg.NodeName+"/assignments", &resp); err != nil {
		a.logger.Warn("poll assignments failed", "err", err)
		return
	}

	// Build a set of desired container names.
	desired := map[string]api.Assignment{}
	for _, asn := range resp.Assignments {
		name := ContainerName(asn.JobName, asn.PlacementID)
		desired[name] = asn
	}

	// List running loom containers.
	running, err := a.listLoomContainers(ctx)
	if err != nil {
		a.logger.Warn("list containers failed", "err", err)
		return
	}
	runningNames := map[string]string{} // name → containerID
	for _, c := range running {
		for _, n := range c.Names {
			clean := strings.TrimPrefix(n, "/")
			runningNames[clean] = c.ID
		}
	}

	// Start missing containers.
	for name, asn := range desired {
		if _, ok := runningNames[name]; ok {
			continue // already running
		}
		if err := a.startContainer(ctx, name, asn); err != nil {
			a.logger.Error("start container", "name", name, "err", err)
		} else {
			a.logger.Info("started container", "name", name, "image", asn.Image)
		}
	}

	// Stop containers that are no longer assigned.
	for name, containerID := range runningNames {
		if _, ok := desired[name]; !ok {
			if err := a.stopContainer(ctx, containerID, name); err != nil {
				a.logger.Error("stop container", "name", name, "err", err)
			} else {
				a.logger.Info("stopped container", "name", name)
			}
		}
	}
}

func (a *Agent) startContainer(ctx context.Context, name string, asn api.Assignment) error {
	// Pull image (best-effort; may already be present).
	rc, err := a.docker.ImagePull(ctx, asn.Image, image.PullOptions{})
	if err == nil {
		io.Copy(io.Discard, rc)
		rc.Close()
	}

	env := asn.Env
	if env == nil {
		env = []string{}
	}

	cfg := &dockertypes.Config{
		Image: asn.Image,
		Env:   env,
	}
	if len(asn.Command) > 0 {
		cfg.Cmd = asn.Command
	}

	hostCfg := &dockertypes.HostConfig{
		RestartPolicy: dockertypes.RestartPolicy{Name: "unless-stopped"},
	}

	resp, err := a.docker.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := a.docker.ContainerStart(ctx, resp.ID, dockertypes.StartOptions{}); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	return nil
}

func (a *Agent) stopContainer(ctx context.Context, containerID, name string) error {
	timeout := 10
	if err := a.docker.ContainerStop(ctx, containerID, dockertypes.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	if err := a.docker.ContainerRemove(ctx, containerID, dockertypes.RemoveOptions{}); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	return nil
}

func (a *Agent) listLoomContainers(ctx context.Context) ([]dockertypes.Summary, error) {
	f := filters.NewArgs(filters.Arg("name", containerPrefix))
	return a.docker.ContainerList(ctx, dockertypes.ListOptions{Filters: f})
}

// ContainerName returns a deterministic container name for a job placement.
func ContainerName(jobName string, placementID int64) string {
	return fmt.Sprintf("%s%s-%d", containerPrefix, jobName, placementID)
}

// --- HTTP helpers ---

func (a *Agent) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.ServerURL+path, nil)
	if err != nil {
		return err
	}
	a.setAuth(req)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (a *Agent) post(ctx context.Context, path string, body any, out any) error {
	return a.doJSON(ctx, http.MethodPost, path, body, out)
}

func (a *Agent) put(ctx context.Context, path string, body any, out any) error {
	return a.doJSON(ctx, http.MethodPut, path, body, out)
}

func (a *Agent) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, a.cfg.ServerURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	a.setAuth(req)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (a *Agent) setAuth(req *http.Request) {
	if a.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	}
}
