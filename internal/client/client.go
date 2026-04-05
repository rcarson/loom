package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rcarson/loom/internal/api"
)

// Context is a single named server target in the loomctl config.
type Context struct {
	Name   string `yaml:"name"`
	Server string `yaml:"server"`
	Token  string `yaml:"token"`
}

// Config is the loomctl context configuration (~/.config/loom/config.yaml).
type Config struct {
	CurrentContext string    `yaml:"current-context"`
	Contexts       []Context `yaml:"contexts"`
}

// Client is an HTTP client that talks to a loom-server using the current context.
type Client struct {
	cfg    *Config
	http   *http.Client
	cfgPath string
}

// LoadConfig reads the loomctl config from path, creating an empty one if absent.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("client: read config %s: %w", path, err)
	}
	// Expand env vars in token fields before parsing.
	var cfg Config
	if err := yaml.Unmarshal([]byte(expandEnv(string(data))), &cfg); err != nil {
		return nil, fmt.Errorf("client: parse config: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the config to path.
func SaveConfig(cfg *Config, path string) error {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return fmt.Errorf("client: mkdir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// New creates a Client from a loaded config.
func New(cfg *Config, cfgPath string) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}, cfgPath: cfgPath}
}

// CurrentContext returns the active context, or an error if none is set.
func (c *Client) CurrentContext() (*Context, error) {
	if c.cfg.CurrentContext == "" {
		return nil, fmt.Errorf("no current context set — run: loomctl context use <name>")
	}
	for i := range c.cfg.Contexts {
		if c.cfg.Contexts[i].Name == c.cfg.CurrentContext {
			return &c.cfg.Contexts[i], nil
		}
	}
	return nil, fmt.Errorf("context %q not found in config", c.cfg.CurrentContext)
}

// UseContext sets the current context by name and saves the config.
func (c *Client) UseContext(name string) error {
	for _, ctx := range c.cfg.Contexts {
		if ctx.Name == name {
			c.cfg.CurrentContext = name
			return SaveConfig(c.cfg, c.cfgPath)
		}
	}
	return fmt.Errorf("context %q not found", name)
}

// AddContext adds or replaces a context and saves the config.
func (c *Client) AddContext(ctx Context) error {
	for i, existing := range c.cfg.Contexts {
		if existing.Name == ctx.Name {
			c.cfg.Contexts[i] = ctx
			return SaveConfig(c.cfg, c.cfgPath)
		}
	}
	c.cfg.Contexts = append(c.cfg.Contexts, ctx)
	return SaveConfig(c.cfg, c.cfgPath)
}

// ListContexts returns all configured contexts.
func (c *Client) ListContexts() []Context {
	return c.cfg.Contexts
}

// --- API methods ---

func (c *Client) ListNodes() ([]api.NodeResponse, error) {
	var nodes []api.NodeResponse
	err := c.get("/api/v1/nodes", &nodes)
	return nodes, err
}

func (c *Client) GetNode(name string) (*api.NodeResponse, error) {
	var n api.NodeResponse
	err := c.get("/api/v1/nodes/"+name, &n)
	return &n, err
}

func (c *Client) SubmitJob(specYAML string) error {
	return c.post("/api/v1/jobs", api.SubmitJobRequest{SpecYAML: specYAML}, nil)
}

func (c *Client) ListJobs() ([]api.JobResponse, error) {
	var jobs []api.JobResponse
	err := c.get("/api/v1/jobs", &jobs)
	return jobs, err
}

func (c *Client) GetJob(name string) (*api.JobResponse, error) {
	var j api.JobResponse
	err := c.get("/api/v1/jobs/"+name, &j)
	return &j, err
}

func (c *Client) DeleteJob(name string) error {
	return c.delete("/api/v1/jobs/" + name)
}

// --- HTTP helpers ---

func (c *Client) get(path string, out any) error {
	ctx, err := c.CurrentContext()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, ctx.Server+path, nil)
	if err != nil {
		return err
	}
	setAuth(req, ctx.Token)
	return c.do(req, out)
}

func (c *Client) post(path string, body any, out any) error {
	return c.doJSON(http.MethodPost, path, body, out)
}

func (c *Client) delete(path string) error {
	ctx, err := c.CurrentContext()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, ctx.Server+path, nil)
	if err != nil {
		return err
	}
	setAuth(req, ctx.Token)
	return c.do(req, nil)
}

func (c *Client) doJSON(method, path string, body any, out any) error {
	ctx, err := c.CurrentContext()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req, err := http.NewRequest(method, ctx.Server+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req, ctx.Token)
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e api.ErrorResponse
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("server error: %s", e.Error)
		}
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func setAuth(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

var envVarRe = strings.NewReplacer() // placeholder; actual expansion below

func expandEnv(s string) string {
	// Replace ${VAR} with os.Getenv("VAR")
	result := &strings.Builder{}
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			end := strings.Index(s[i:], "}")
			if end > 0 {
				varName := s[i+2 : i+end]
				result.WriteString(os.Getenv(varName))
				i += end + 1
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
