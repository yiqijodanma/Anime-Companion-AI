package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type ReadinessCheck struct {
	Name  string
	Check func(context.Context) error
}

func RegisterOperationalHealth(router *gin.Engine, checks ...ReadinessCheck) {
	router.GET("/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		for _, check := range checks {
			if check.Check == nil || check.Check(ctx) != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{
						"code":    "dependency_unavailable",
						"message": "service is not ready",
					},
					"dependency": check.Name,
				})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}
