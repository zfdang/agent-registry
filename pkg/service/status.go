package service

import (
	"time"

	"github.com/zfdang/agent-registry/pkg/model/db"
)

// ValidationResultToAgentStatus maps a freshly-computed validation/pipeline
// result onto the agent status vocabulary. It is only valid for a run that
// just happened (checks are freshly stamped, so anyExpired is always false)
// — see RollupAgentStatus for the general form used at read time.
func ValidationResultToAgentStatus(result string) string {
	switch result {
	case db.ValidationPassed:
		return db.StatusVerified
	case db.ValidationPassedWithWarnings:
		return db.StatusVerifiedWithWarnings
	default:
		return db.StatusFailed
	}
}

// EffectiveStatus recomputes an agent's verification_status from its live
// checks at read time, so a check's expiry can flip the displayed status
// to "stale" without requiring a write (03 §Freshness). storedStatus is the
// fallback when there are no checks at all (shouldn't happen in practice).
func EffectiveStatus(checks []db.VerificationCheck, storedStatus string) string {
	if len(checks) == 0 {
		return storedStatus
	}

	var l1, l2, l3 string
	now := time.Now().UTC()
	anyExpired := false

	for _, c := range checks {
		switch c.Kind {
		case db.CheckL1Card:
			l1 = c.Status
		case db.CheckL2ID:
			l2 = c.Status
		case db.CheckL3Protocol:
			l3 = c.Status
		}
		if now.After(c.ExpiresAt) {
			anyExpired = true
		}
	}

	return RollupAgentStatus(l1, l2, l3, anyExpired)
}
