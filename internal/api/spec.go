package api

// WorkloadType identifies the kind of workload a job spec describes.
type WorkloadType string

const (
	TypeService  WorkloadType = "service"
	TypeFunction WorkloadType = "function"
	TypeJob      WorkloadType = "job"
)

// SpreadStrategy controls how replicas are distributed across nodes.
type SpreadStrategy string

const (
	SpreadPack   SpreadStrategy = "pack"
	SpreadRegion SpreadStrategy = "region"
	SpreadZone   SpreadStrategy = "zone"
)

// JobSpec is the top-level structure for a Loom workload definition.
type JobSpec struct {
	Name        string            `yaml:"name"`
	Type        WorkloadType      `yaml:"type"`
	Image       string            `yaml:"image"`
	Port        int               `yaml:"port,omitempty"`
	Command     []string          `yaml:"command,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Replicas    int               `yaml:"replicas,omitempty"`
	Placement   PlacementSpec     `yaml:"placement,omitempty"`
	Healthcheck *HealthcheckSpec  `yaml:"healthcheck,omitempty"`
	Timeout     string            `yaml:"timeout,omitempty"` // for functions
	Schedule    string            `yaml:"schedule,omitempty"` // cron for jobs
}

// PlacementSpec controls how the scheduler places replicas across the cluster.
type PlacementSpec struct {
	Spread  SpreadStrategy `yaml:"spread,omitempty"`
	Regions []string       `yaml:"regions,omitempty"`
	Tags    []string       `yaml:"tags,omitempty"`
}

// HealthcheckSpec defines an HTTP health check for service workloads.
type HealthcheckSpec struct {
	Path     string `yaml:"path"`
	Interval string `yaml:"interval,omitempty"`
	Timeout  string `yaml:"timeout,omitempty"`
}
