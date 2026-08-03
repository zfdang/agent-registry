package service

import "github.com/zfdang/agent-registry/pkg/model/db"

// RollupValidationResult computes the pre-registration validation result
// (05 §roll-up): l1/l3 failure fails the whole run; l2 is advisory only.
func RollupValidationResult(l1, l2, l3 string) string {
	if l1 == db.CheckStatusFailed || l3 == db.CheckStatusFailed {
		return db.ValidationFailed
	}
	if l2 == db.CheckStatusWarning {
		return db.ValidationPassedWithWarnings
	}
	return db.ValidationPassed
}

// RollupAgentStatus computes the denormalized agents.verification_status
// (03 §roll-up). Failure takes precedence over staleness, which takes
// precedence over the L2 warning.
func RollupAgentStatus(l1, l2, l3 string, anyExpired bool) string {
	if l1 == db.CheckStatusFailed || l3 == db.CheckStatusFailed {
		return db.StatusFailed
	}
	if anyExpired {
		return db.StatusStale
	}
	if l2 == db.CheckStatusWarning {
		return db.StatusVerifiedWithWarnings
	}
	return db.StatusVerified
}

// Registerable reports whether a validation result permits registration.
func Registerable(result string) bool {
	return result != db.ValidationFailed
}
