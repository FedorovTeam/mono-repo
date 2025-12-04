package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterHealthz(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}
