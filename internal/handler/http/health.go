package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterHealth mounts the unauthenticated liveness probe.
func RegisterHealth(r *gin.Engine) {
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
}
