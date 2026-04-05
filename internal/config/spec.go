package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rcarson/loom/internal/api"
)

var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// ParseJobSpec parses and validates a job spec from raw YAML bytes.
// Environment variable references in the form ${VAR} are expanded.
func ParseJobSpec(data []byte) (*api.JobSpec, error) {
	expanded := expandEnv(string(data))

	var spec api.JobSpec
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse job spec: %w", err)
	}

	if err := validateJobSpec(&spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func validateJobSpec(s *api.JobSpec) error {
	if s.Name == "" {
		return fmt.Errorf("job spec: name is required")
	}
	if s.Image == "" {
		return fmt.Errorf("job spec: image is required")
	}

	switch s.Type {
	case api.TypeService, api.TypeFunction, api.TypeJob:
	case "":
		return fmt.Errorf("job spec: type is required")
	default:
		return fmt.Errorf("job spec: unknown type %q (must be service, function, or job)", s.Type)
	}

	if s.Replicas < 0 {
		return fmt.Errorf("job spec: replicas must be >= 0")
	}
	if s.Replicas == 0 {
		s.Replicas = 1
	}

	switch s.Placement.Spread {
	case api.SpreadPack, api.SpreadRegion, api.SpreadZone, "":
	default:
		return fmt.Errorf("job spec: unknown placement spread %q (must be pack, region, or zone)", s.Placement.Spread)
	}
	if s.Placement.Spread == "" {
		s.Placement.Spread = api.SpreadPack
	}

	if s.Type == api.TypeFunction && s.Port == 0 {
		return fmt.Errorf("job spec: port is required for function workloads")
	}

	return nil
}

// expandEnv replaces ${VAR} references with the corresponding environment variable value.
// Unset variables resolve to an empty string.
func expandEnv(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		return os.Getenv(name)
	})
}
