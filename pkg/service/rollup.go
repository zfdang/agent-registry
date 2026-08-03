package service

import "github.com/zfdang/agent-registry/pkg/model/db"

// RollupValidationResult computes the pre-registration validation result
// (05 §roll-up): both checks are gating — either failure fails the run.
func RollupValidationResult(l1, l2 string) string {
	if l1 == db.CheckStatusFailed || l2 == db.CheckStatusFailed {
		return db.ValidationFailed
	}
	return db.ValidationPassed
}

// RollupAgentStatus computes the denormalized agents.verification_status
// (03 §roll-up). Failure takes precedence over staleness.
func RollupAgentStatus(l1, l2 string, anyExpired bool) string {
	if l1 == db.CheckStatusFailed || l2 == db.CheckStatusFailed {
		return db.StatusFailed
	}
	if anyExpired {
		return db.StatusStale
	}
	return db.StatusVerified
}

// Registerable reports whether a validation result permits registration.
func Registerable(result string) bool {
	return result != db.ValidationFailed
}
