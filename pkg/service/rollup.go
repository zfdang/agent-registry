package service

import "github.com/zfdang/agent-registry/pkg/model/db"

// RollupValidationResult requires a valid card and distinguishes a failed
// endpoint probe from full verification.
func RollupValidationResult(l1, l2 string) string {
	if l1 != db.CheckStatusPassed {
		return db.ValidationFailed
	}
	if l2 != db.CheckStatusPassed {
		return db.ValidationPartial
	}
	return db.ValidationPassed
}

// RollupAgentStatus recomputes the listing status from its latest checks.
// Only a previously verified listing can become stale.
func RollupAgentStatus(l1, l2 string, anyExpired bool) string {
	status := ValidationResultToAgentStatus(RollupValidationResult(l1, l2))
	if status == db.StatusVerified && anyExpired {
		return db.StatusStale
	}
	return status
}

// Registerable reports whether a validation result permits registration.
func Registerable(result string) bool {
	return result == db.ValidationPassed || result == db.ValidationPartial
}
