package db

import "github.com/google/uuid"

// Prefixed-UUID primary keys — self-describing in logs and URLs (02).
const (
	AgentIDPrefix      = "agt_"
	CheckIDPrefix      = "chk_"
	ValidationIDPrefix = "val_"
	SnapshotIDPrefix   = "snap_"
	TaskIDPrefix       = "task_"
)

// NewID returns a new prefixed-UUID identifier, e.g. "agt_<uuid>".
func NewID(prefix string) string {
	return prefix + uuid.NewString()
}
