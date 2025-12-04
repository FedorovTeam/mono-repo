package httpapi

import (
	"net/http"
	"template-service/pkg/config"
	"time"

	"template-service/internal/usecase"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r *gin.Engine, c *config.Config, u *usecase.DemoUseCase) {
	r.POST("/demo", func(c *gin.Context) {
		var req struct {
			BucketID string `json:"bucket_id"`
			RegionID string `json:"region_id"`
			Value    string `json:"value" binding:"required"`
			UserID   string `json:"user_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		traceID := c.GetHeader("X-Trace-Id")
		t := time.Now()
		entity, err := u.CreateDemo(
			req.UserID,
			req.Value,
			"",     // filePath генерируется внутри usecase или можно пробросить
			"demo", // bucketType
			"demo", // prefix
			"",     // entityPath
			req.RegionID,
			t,
			traceID,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, entity)
	})

	r.GET("/demo/:id", func(c *gin.Context) {
		id := c.Param("id")
		entity, err := u.GetDemo(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, entity)
	})
}
