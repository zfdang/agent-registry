package service

import (
	"errors"
	"fmt"

	"github.com/zfdang/agent-registry/pkg/model"
)

// Sentinel errors returned by the service layer. Adapters (handler,
// mcpserver, a2aserver) each map these to their own protocol's error shape
// — see docs/04, docs/06, docs/07.
var (
	ErrNotFound           = errors.New("not_found")
	ErrInvalidURL         = errors.New("invalid_url")
	ErrValidationNotFound = errors.New("validation_not_found")
	ErrValidationExpired  = errors.New("validation_expired")
	ErrValidationConsumed = errors.New("validation_consumed")
	ErrValidationFailed   = errors.New("validation_failed")
	ErrTaskNotFound       = errors.New("task_not_found")
	ErrTaskNotCancelable  = errors.New("task_not_cancelable")
	ErrUnknownSkill       = errors.New("unknown_skill")
	ErrInvalidInput       = errors.New("invalid_input")
)

// AmbiguousRefError signals that a ref (usually an agent_id) matched more
// than one agent (04). Candidates lets the caller retry with a URL.
type AmbiguousRefError struct {
	Candidates []model.AgentCandidate
}

func (e *AmbiguousRefError) Error() string {
	return fmt.Sprintf("ambiguous_ref: %d candidates", len(e.Candidates))
}
