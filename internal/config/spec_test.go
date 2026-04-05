package config_test

import (
	"os"
	"testing"

	"github.com/rcarson/loom/internal/api"
	"github.com/rcarson/loom/internal/config"
)

func TestParseJobSpec_Service(t *testing.T) {
	data := []byte(`
name: myapp
type: service
image: ghcr.io/me/myapp:v1
port: 8080
replicas: 2
placement:
  spread: region
  regions: [home]
healthcheck:
  path: /healthz
  interval: 30s
`)
	spec, err := config.ParseJobSpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Name != "myapp" {
		t.Errorf("name: got %q want %q", spec.Name, "myapp")
	}
	if spec.Type != api.TypeService {
		t.Errorf("type: got %q want %q", spec.Type, api.TypeService)
	}
	if spec.Replicas != 2 {
		t.Errorf("replicas: got %d want 2", spec.Replicas)
	}
	if spec.Placement.Spread != api.SpreadRegion {
		t.Errorf("spread: got %q want %q", spec.Placement.Spread, api.SpreadRegion)
	}
	if spec.Healthcheck == nil || spec.Healthcheck.Path != "/healthz" {
		t.Errorf("healthcheck.path: got %v", spec.Healthcheck)
	}
}

func TestParseJobSpec_Function(t *testing.T) {
	data := []byte(`
name: resize-image
type: function
image: ghcr.io/me/resize-fn:v1
port: 8080
timeout: 30s
`)
	spec, err := config.ParseJobSpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Type != api.TypeFunction {
		t.Errorf("type: got %q want %q", spec.Type, api.TypeFunction)
	}
	if spec.Port != 8080 {
		t.Errorf("port: got %d want 8080", spec.Port)
	}
}

func TestParseJobSpec_Job(t *testing.T) {
	data := []byte(`
name: db-migrate
type: job
image: ghcr.io/me/myapp:v1
command: ["./migrate", "up"]
schedule: "0 2 * * *"
`)
	spec, err := config.ParseJobSpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Type != api.TypeJob {
		t.Errorf("type: got %q want %q", spec.Type, api.TypeJob)
	}
	if spec.Schedule != "0 2 * * *" {
		t.Errorf("schedule: got %q", spec.Schedule)
	}
}

func TestParseJobSpec_DefaultReplicas(t *testing.T) {
	data := []byte(`
name: myapp
type: service
image: ghcr.io/me/myapp:v1
`)
	spec, err := config.ParseJobSpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Replicas != 1 {
		t.Errorf("default replicas: got %d want 1", spec.Replicas)
	}
	if spec.Placement.Spread != api.SpreadPack {
		t.Errorf("default spread: got %q want %q", spec.Placement.Spread, api.SpreadPack)
	}
}

func TestParseJobSpec_EnvVarExpansion(t *testing.T) {
	t.Setenv("DB_URL", "postgres://localhost/mydb")
	data := []byte(`
name: myapp
type: service
image: ghcr.io/me/myapp:v1
env:
  DATABASE_URL: ${DB_URL}
`)
	spec, err := config.ParseJobSpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Env["DATABASE_URL"] != "postgres://localhost/mydb" {
		t.Errorf("env expansion: got %q", spec.Env["DATABASE_URL"])
	}
}

func TestParseJobSpec_UnsetEnvVar(t *testing.T) {
	os.Unsetenv("UNSET_VAR")
	data := []byte(`
name: myapp
type: service
image: ghcr.io/me/myapp:v1
env:
  THING: ${UNSET_VAR}
`)
	spec, err := config.ParseJobSpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Env["THING"] != "" {
		t.Errorf("unset env var: got %q want empty string", spec.Env["THING"])
	}
}

func TestParseJobSpec_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing name",
			yaml: "type: service\nimage: foo:v1\n",
			want: "name is required",
		},
		{
			name: "missing image",
			yaml: "name: myapp\ntype: service\n",
			want: "image is required",
		},
		{
			name: "missing type",
			yaml: "name: myapp\nimage: foo:v1\n",
			want: "type is required",
		},
		{
			name: "unknown type",
			yaml: "name: myapp\nimage: foo:v1\ntype: daemon\n",
			want: "unknown type",
		},
		{
			name: "unknown spread",
			yaml: "name: myapp\nimage: foo:v1\ntype: service\nplacement:\n  spread: random\n",
			want: "unknown placement spread",
		},
		{
			name: "function missing port",
			yaml: "name: myfn\nimage: foo:v1\ntype: function\n",
			want: "port is required",
		},
		{
			name: "negative replicas",
			yaml: "name: myapp\nimage: foo:v1\ntype: service\nreplicas: -1\n",
			want: "replicas must be",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.ParseJobSpec([]byte(tc.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseJobSpec_UnknownField(t *testing.T) {
	data := []byte(`
name: myapp
type: service
image: foo:v1
unknown_field: bad
`)
	_, err := config.ParseJobSpec(data)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
