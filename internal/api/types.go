package api

import "time"

// RegisterNodeRequest is sent by loom-agent on startup.
type RegisterNodeRequest struct {
	Name     string `json:"name"`
	Region   string `json:"region"`
	Zone     string `json:"zone"`
	Tags     string `json:"tags"` // comma-separated
	CPUCores int    `json:"cpu_cores"`
	MemoryMB int    `json:"memory_mb"`
}

// HeartbeatRequest is sent by loom-agent periodically.
type HeartbeatRequest struct {
	ContainerIDs []string `json:"container_ids"` // currently running loom containers
}

// NodeResponse is the API representation of a node.
type NodeResponse struct {
	Name          string    `json:"name"`
	Region        string    `json:"region"`
	Zone          string    `json:"zone"`
	Tags          string    `json:"tags"`
	CPUCores      int       `json:"cpu_cores"`
	MemoryMB      int       `json:"memory_mb"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	RegisteredAt  time.Time `json:"registered_at"`
}

// SubmitJobRequest is sent by loomctl to submit or update a job.
type SubmitJobRequest struct {
	SpecYAML string `json:"spec_yaml"`
}

// JobResponse is the API representation of a job.
type JobResponse struct {
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Placements []PlacementResponse `json:"placements"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

// PlacementResponse is the API representation of a job→node placement.
type PlacementResponse struct {
	ID          int64     `json:"id"`
	NodeName    string    `json:"node_name"`
	ContainerID string    `json:"container_id"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AssignmentsResponse is returned to an agent when it polls for its work.
type AssignmentsResponse struct {
	Assignments []Assignment `json:"assignments"`
}

// Assignment describes a single workload the agent should run.
type Assignment struct {
	PlacementID int64    `json:"placement_id"`
	JobName     string   `json:"job_name"`
	Image       string   `json:"image"`
	Port        int      `json:"port,omitempty"`
	Command     []string `json:"command,omitempty"`
	Env         []string `json:"env"` // KEY=VALUE pairs
}

// ErrorResponse is returned for all API errors.
type ErrorResponse struct {
	Error string `json:"error"`
}
