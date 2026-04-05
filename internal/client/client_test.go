package client_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rcarson/loom/internal/client"
)

func tempConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.yaml")
}

func TestLoadConfig_Empty(t *testing.T) {
	cfg, err := client.LoadConfig(tempConfigPath(t))
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if cfg.CurrentContext != "" || len(cfg.Contexts) != 0 {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	path := tempConfigPath(t)
	cfg := &client.Config{
		CurrentContext: "home",
		Contexts: []client.Context{
			{Name: "home", Server: "http://delamain:8080", Token: "secret"},
		},
	}
	if err := client.SaveConfig(cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := client.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.CurrentContext != "home" {
		t.Errorf("current-context: got %q want home", loaded.CurrentContext)
	}
	if len(loaded.Contexts) != 1 || loaded.Contexts[0].Server != "http://delamain:8080" {
		t.Errorf("contexts: %+v", loaded.Contexts)
	}
}

func TestAddContext(t *testing.T) {
	path := tempConfigPath(t)
	c := client.New(&client.Config{}, path)
	if err := c.AddContext(client.Context{Name: "home", Server: "http://host1:8080"}); err != nil {
		t.Fatalf("add context: %v", err)
	}
	if err := c.AddContext(client.Context{Name: "work", Server: "http://host2:8080"}); err != nil {
		t.Fatalf("add context: %v", err)
	}
	contexts := c.ListContexts()
	if len(contexts) != 2 {
		t.Errorf("got %d contexts, want 2", len(contexts))
	}
	// Re-adding the same name updates it.
	if err := c.AddContext(client.Context{Name: "home", Server: "http://host1-updated:8080"}); err != nil {
		t.Fatalf("update context: %v", err)
	}
	contexts = c.ListContexts()
	if len(contexts) != 2 {
		t.Errorf("duplicate context created: got %d, want 2", len(contexts))
	}
	if contexts[0].Server != "http://host1-updated:8080" {
		t.Errorf("context not updated: %+v", contexts[0])
	}
}

func TestUseContext(t *testing.T) {
	path := tempConfigPath(t)
	cfg := &client.Config{
		Contexts: []client.Context{
			{Name: "home", Server: "http://host1:8080"},
			{Name: "work", Server: "http://host2:8080"},
		},
	}
	client.SaveConfig(cfg, path)
	c := client.New(cfg, path)

	if err := c.UseContext("work"); err != nil {
		t.Fatalf("use context: %v", err)
	}
	ctx, err := c.CurrentContext()
	if err != nil {
		t.Fatalf("current context: %v", err)
	}
	if ctx.Name != "work" {
		t.Errorf("current context: got %q want work", ctx.Name)
	}
}

func TestUseContext_NotFound(t *testing.T) {
	path := tempConfigPath(t)
	c := client.New(&client.Config{}, path)
	if err := c.UseContext("missing"); err == nil {
		t.Fatal("expected error for missing context")
	}
}

func TestCurrentContext_NotSet(t *testing.T) {
	c := client.New(&client.Config{}, tempConfigPath(t))
	_, err := c.CurrentContext()
	if err == nil {
		t.Fatal("expected error when no current context set")
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	t.Setenv("MY_LOOM_TOKEN", "tok123")
	path := tempConfigPath(t)
	yaml := "current-context: home\ncontexts:\n  - name: home\n    server: http://host:8080\n    token: ${MY_LOOM_TOKEN}\n"
	os.WriteFile(path, []byte(yaml), 0o600)

	cfg, err := client.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Contexts[0].Token != "tok123" {
		t.Errorf("token not expanded: got %q", cfg.Contexts[0].Token)
	}
}

func TestListContexts_Empty(t *testing.T) {
	c := client.New(&client.Config{}, tempConfigPath(t))
	if len(c.ListContexts()) != 0 {
		t.Error("expected empty list")
	}
}
