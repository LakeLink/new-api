package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders applies browser protections that are safe for API, relay,
// embedded frontend, and OAuth popup responses alike.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
		c.Next()
	}
}
