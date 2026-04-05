package agent_test

import (
	"testing"

	"github.com/rcarson/loom/internal/agent"
)

func TestContainerName(t *testing.T) {
	cases := []struct {
		jobName     string
		placementID int64
		want        string
	}{
		{"myapp", 1, "loom-myapp-1"},
		{"resize-image", 42, "loom-resize-image-42"},
		{"db-migrate", 100, "loom-db-migrate-100"},
	}
	for _, tc := range cases {
		got := agent.ContainerName(tc.jobName, tc.placementID)
		if got != tc.want {
			t.Errorf("ContainerName(%q, %d) = %q, want %q", tc.jobName, tc.placementID, got, tc.want)
		}
	}
}
