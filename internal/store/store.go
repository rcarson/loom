package store

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// NodeStatus represents the health state of a registered node.
type NodeStatus string

const (
	NodeStatusHealthy   NodeStatus = "healthy"
	NodeStatusUnhealthy NodeStatus = "unhealthy"
)

// PlacementStatus represents the state of a job placement on a node.
type PlacementStatus string

const (
	PlacementStatusPending  PlacementStatus = "pending"
	PlacementStatusRunning  PlacementStatus = "running"
	PlacementStatusStopped  PlacementStatus = "stopped"
	PlacementStatusFailed   PlacementStatus = "failed"
)

// Node represents a registered loom-agent node.
type Node struct {
	Name          string
	Region        string
	Zone          string
	Tags          string // comma-separated
	CPUCores      int
	MemoryMB      int
	Status        NodeStatus
	LastHeartbeat time.Time
	RegisteredAt  time.Time
}

// Job represents a submitted workload definition.
type Job struct {
	Name      string
	Type      string
	SpecYAML  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Placement represents a job→node assignment.
type Placement struct {
	ID          int64
	JobName     string
	NodeName    string
	ContainerID string
	Status      PlacementStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Event represents an audit log entry.
type Event struct {
	ID        int64
	Type      string
	JobName   string
	NodeName  string
	Message   string
	CreatedAt time.Time
}

// Store is the SQLite-backed state store for loom-server.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

// Open opens (or creates) the SQLite database at the given path and runs schema migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// Single writer to avoid SQLITE_BUSY under concurrent access.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			name           TEXT PRIMARY KEY,
			region         TEXT NOT NULL DEFAULT '',
			zone           TEXT NOT NULL DEFAULT '',
			tags           TEXT NOT NULL DEFAULT '',
			cpu_cores      INTEGER NOT NULL DEFAULT 0,
			memory_mb      INTEGER NOT NULL DEFAULT 0,
			status         TEXT NOT NULL DEFAULT 'healthy',
			last_heartbeat DATETIME NOT NULL,
			registered_at  DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS jobs (
			name       TEXT PRIMARY KEY,
			type       TEXT NOT NULL,
			spec_yaml  TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS placements (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			job_name     TEXT NOT NULL,
			node_name    TEXT NOT NULL,
			container_id TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'pending',
			created_at   DATETIME NOT NULL,
			updated_at   DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			type       TEXT NOT NULL,
			job_name   TEXT NOT NULL DEFAULT '',
			node_name  TEXT NOT NULL DEFAULT '',
			message    TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// UpsertNode inserts or replaces a node record.
func (s *Store) UpsertNode(n *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO nodes (name, region, zone, tags, cpu_cores, memory_mb, status, last_heartbeat, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			region=excluded.region, zone=excluded.zone, tags=excluded.tags,
			cpu_cores=excluded.cpu_cores, memory_mb=excluded.memory_mb,
			status=excluded.status, last_heartbeat=excluded.last_heartbeat
	`, n.Name, n.Region, n.Zone, n.Tags, n.CPUCores, n.MemoryMB, n.Status, n.LastHeartbeat, n.RegisteredAt)
	if err != nil {
		return fmt.Errorf("store: upsert node %s: %w", n.Name, err)
	}
	return nil
}

// UpdateNodeHeartbeat updates the last_heartbeat timestamp and status for a node.
func (s *Store) UpdateNodeHeartbeat(name string, status NodeStatus, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`UPDATE nodes SET last_heartbeat=?, status=? WHERE name=?`,
		at, status, name,
	)
	if err != nil {
		return fmt.Errorf("store: heartbeat node %s: %w", name, err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("store: node %q not found", name)
	}
	return nil
}

// GetNode returns a node by name, or an error if not found.
func (s *Store) GetNode(name string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row := s.db.QueryRow(
		`SELECT name, region, zone, tags, cpu_cores, memory_mb, status, last_heartbeat, registered_at FROM nodes WHERE name=?`,
		name,
	)
	return scanNode(row)
}

// ListNodes returns all nodes.
func (s *Store) ListNodes() ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT name, region, zone, tags, cpu_cores, memory_mb, status, last_heartbeat, registered_at FROM nodes ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list nodes: %w", err)
	}
	defer rows.Close()
	return collectNodes(rows)
}

// MarkStaleNodesUnhealthy marks nodes whose last heartbeat is older than the cutoff as unhealthy.
func (s *Store) MarkStaleNodesUnhealthy(cutoff time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE nodes SET status=? WHERE last_heartbeat < ? AND status=?`,
		NodeStatusUnhealthy, cutoff, NodeStatusHealthy,
	)
	if err != nil {
		return fmt.Errorf("store: mark stale nodes: %w", err)
	}
	return nil
}

// UpsertJob inserts or replaces a job record.
func (s *Store) UpsertJob(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO jobs (name, type, spec_yaml, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			type=excluded.type, spec_yaml=excluded.spec_yaml, updated_at=?
	`, j.Name, j.Type, j.SpecYAML, now, now, now)
	if err != nil {
		return fmt.Errorf("store: upsert job %s: %w", j.Name, err)
	}
	return nil
}

// GetJob returns a job by name, or an error if not found.
func (s *Store) GetJob(name string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row := s.db.QueryRow(
		`SELECT name, type, spec_yaml, created_at, updated_at FROM jobs WHERE name=?`,
		name,
	)
	return scanJob(row)
}

// ListJobs returns all jobs ordered by name.
func (s *Store) ListJobs() ([]*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT name, type, spec_yaml, created_at, updated_at FROM jobs ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer rows.Close()
	return collectJobs(rows)
}

// DeleteJob removes a job and all its placements.
func (s *Store) DeleteJob(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM placements WHERE job_name=?`, name)
	if err != nil {
		return fmt.Errorf("store: delete placements for job %s: %w", name, err)
	}
	_, err = s.db.Exec(`DELETE FROM jobs WHERE name=?`, name)
	if err != nil {
		return fmt.Errorf("store: delete job %s: %w", name, err)
	}
	return nil
}

// CreatePlacement inserts a new placement record.
func (s *Store) CreatePlacement(p *Placement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		INSERT INTO placements (job_name, node_name, container_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, p.JobName, p.NodeName, p.ContainerID, p.Status, now, now)
	if err != nil {
		return fmt.Errorf("store: create placement: %w", err)
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

// UpdatePlacementStatus updates the status and container ID of a placement.
func (s *Store) UpdatePlacementStatus(id int64, status PlacementStatus, containerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE placements SET status=?, container_id=?, updated_at=? WHERE id=?`,
		status, containerID, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("store: update placement %d: %w", id, err)
	}
	return nil
}

// ListPlacementsForJob returns all placements for a given job.
func (s *Store) ListPlacementsForJob(jobName string) ([]*Placement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, job_name, node_name, container_id, status, created_at, updated_at FROM placements WHERE job_name=? ORDER BY id`,
		jobName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list placements for job %s: %w", jobName, err)
	}
	defer rows.Close()
	return collectPlacements(rows)
}

// ListPlacementsForNode returns all active placements assigned to a node.
func (s *Store) ListPlacementsForNode(nodeName string) ([]*Placement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, job_name, node_name, container_id, status, created_at, updated_at FROM placements WHERE node_name=? AND status NOT IN ('stopped') ORDER BY id`,
		nodeName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list placements for node %s: %w", nodeName, err)
	}
	defer rows.Close()
	return collectPlacements(rows)
}

// DeletePlacementsForNode removes all placements assigned to a node (used when rescheduling after failure).
func (s *Store) DeletePlacementsForNode(nodeName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM placements WHERE node_name=?`, nodeName)
	if err != nil {
		return fmt.Errorf("store: delete placements for node %s: %w", nodeName, err)
	}
	return nil
}

// AppendEvent inserts a new audit log event.
func (s *Store) AppendEvent(e *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		INSERT INTO events (type, job_name, node_name, message, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, e.Type, e.JobName, e.NodeName, e.Message, now)
	if err != nil {
		return fmt.Errorf("store: append event: %w", err)
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt = now
	return nil
}

// ListEvents returns the most recent n events, newest first.
func (s *Store) ListEvents(n int) ([]*Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, type, job_name, node_name, message, created_at FROM events ORDER BY id DESC LIMIT ?`,
		n,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	defer rows.Close()
	var events []*Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Type, &e.JobName, &e.NodeName, &e.Message, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}

// --- scan helpers ---

func scanNode(row *sql.Row) (*Node, error) {
	var n Node
	err := row.Scan(&n.Name, &n.Region, &n.Zone, &n.Tags, &n.CPUCores, &n.MemoryMB, &n.Status, &n.LastHeartbeat, &n.RegisteredAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: node not found")
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan node: %w", err)
	}
	return &n, nil
}

func collectNodes(rows *sql.Rows) ([]*Node, error) {
	var nodes []*Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.Name, &n.Region, &n.Zone, &n.Tags, &n.CPUCores, &n.MemoryMB, &n.Status, &n.LastHeartbeat, &n.RegisteredAt); err != nil {
			return nil, fmt.Errorf("store: scan node: %w", err)
		}
		nodes = append(nodes, &n)
	}
	return nodes, rows.Err()
}

func scanJob(row *sql.Row) (*Job, error) {
	var j Job
	err := row.Scan(&j.Name, &j.Type, &j.SpecYAML, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: job not found")
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan job: %w", err)
	}
	return &j, nil
}

func collectJobs(rows *sql.Rows) ([]*Job, error) {
	var jobs []*Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.Name, &j.Type, &j.SpecYAML, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan job: %w", err)
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

func collectPlacements(rows *sql.Rows) ([]*Placement, error) {
	var placements []*Placement
	for rows.Next() {
		var p Placement
		if err := rows.Scan(&p.ID, &p.JobName, &p.NodeName, &p.ContainerID, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan placement: %w", err)
		}
		placements = append(placements, &p)
	}
	return placements, rows.Err()
}
