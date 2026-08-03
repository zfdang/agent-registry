// Package handler holds gin REST handlers — protocol adapters over
// pkg/service (02, 04). No business logic lives here.
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zfdang/agent-registry/pkg/model"
	"github.com/zfdang/agent-registry/pkg/service"
)

// writeError maps a service sentinel error to the uniform REST error
// envelope and status code (04's status → error code map).
func writeError(c *gin.Context, err error) {
	var ambiguous *service.AmbiguousRefError
	if errors.As(err, &ambiguous) {
		c.JSON(http.StatusConflict, model.ErrorResponse{Error: model.ErrorBody{
			Code:       "ambiguous_ref",
			Message:    "multiple agents match this agent_id — use the agent URL",
			Candidates: ambiguous.Candidates,
		}})
		return
	}

	switch {
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, errBody("not_found", "no agent matches this reference"))
	case errors.Is(err, service.ErrInvalidURL):
		c.JSON(http.StatusBadRequest, errBody("invalid_url", "url must be an absolute https URL"))
	case errors.Is(err, service.ErrValidationNotFound):
		c.JSON(http.StatusNotFound, errBody("validation_not_found", "unknown validation_id"))
	case errors.Is(err, service.ErrValidationExpired):
		c.JSON(http.StatusGone, errBody("validation_expired", "validation expired — re-validate"))
	case errors.Is(err, service.ErrValidationConsumed):
		c.JSON(http.StatusConflict, errBody("validation_consumed", "validation already used"))
	case errors.Is(err, service.ErrValidationFailed):
		c.JSON(http.StatusUnprocessableEntity, errBody("validation_failed", "validation did not pass"))
	default:
		c.JSON(http.StatusInternalServerError, errBody("internal_error", "unexpected server error"))
	}
}

func errBody(code, message string) model.ErrorResponse {
	return model.ErrorResponse{Error: model.ErrorBody{Code: code, Message: message}}
}
