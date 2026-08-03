package service

import (
	"testing"

	"github.com/zfdang/agent-registry/pkg/model/db"
)

func TestRollupValidationResult(t *testing.T) {
	tests := []struct {
		name       string
		l1, l2, l3 string
		want       string
	}{
		{"all passed", db.CheckStatusPassed, db.CheckStatusPassed, db.CheckStatusPassed, db.ValidationPassed},
		{"l2 warning only", db.CheckStatusPassed, db.CheckStatusWarning, db.CheckStatusPassed, db.ValidationPassedWithWarnings},
		{"l1 failed dominates", db.CheckStatusFailed, db.CheckStatusWarning, db.CheckStatusPassed, db.ValidationFailed},
		{"l3 failed dominates", db.CheckStatusPassed, db.CheckStatusPassed, db.CheckStatusFailed, db.ValidationFailed},
		{"l1 and l3 failed", db.CheckStatusFailed, "", db.CheckStatusFailed, db.ValidationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RollupValidationResult(tt.l1, tt.l2, tt.l3); got != tt.want {
				t.Errorf("RollupValidationResult(%q,%q,%q) = %q, want %q", tt.l1, tt.l2, tt.l3, got, tt.want)
			}
		})
	}
}

func TestRollupAgentStatus(t *testing.T) {
	tests := []struct {
		name       string
		l1, l2, l3 string
		expired    bool
		want       string
	}{
		{"all passed, fresh", db.CheckStatusPassed, db.CheckStatusPassed, db.CheckStatusPassed, false, db.StatusVerified},
		{"l2 warning, fresh", db.CheckStatusPassed, db.CheckStatusWarning, db.CheckStatusPassed, false, db.StatusVerifiedWithWarnings},
		{"expired flips to stale", db.CheckStatusPassed, db.CheckStatusPassed, db.CheckStatusPassed, true, db.StatusStale},
		{"failed takes precedence over expired", db.CheckStatusFailed, db.CheckStatusPassed, db.CheckStatusPassed, true, db.StatusFailed},
		{"failed takes precedence over warning", db.CheckStatusFailed, db.CheckStatusWarning, db.CheckStatusPassed, false, db.StatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RollupAgentStatus(tt.l1, tt.l2, tt.l3, tt.expired); got != tt.want {
				t.Errorf("RollupAgentStatus(%q,%q,%q,%v) = %q, want %q", tt.l1, tt.l2, tt.l3, tt.expired, got, tt.want)
			}
		})
	}
}

func TestRegisterable(t *testing.T) {
	if !Registerable(db.ValidationPassed) {
		t.Error("passed should be registerable")
	}
	if !Registerable(db.ValidationPassedWithWarnings) {
		t.Error("passed_with_warnings should be registerable")
	}
	if Registerable(db.ValidationFailed) {
		t.Error("failed should not be registerable")
	}
}
