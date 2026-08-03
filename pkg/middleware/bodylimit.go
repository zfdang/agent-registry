package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxRequestBody caps inbound request bodies (validate/register/a2a
// payloads are all small JSON documents; there is no file upload surface).
const MaxRequestBody = 1 << 20 // 1 MiB

// BodyLimit rejects oversized request bodies before a handler reads them.
func BodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxRequestBody)
		c.Next()
	}
}
