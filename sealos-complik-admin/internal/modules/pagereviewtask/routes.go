package pagereviewtask

import (
	"github.com/gin-gonic/gin"
	"sealos-complik-admin/internal/infra/database"
)

func InitRoutes(g *gin.Engine) {
	repository := NewRepository(database.Get())
	service := NewService(repository)
	handler := NewHandler(service)

	g.POST("/api/page-review-tasks", handler.Enqueue)
	g.POST("/api/page-review-tasks/claim", handler.Claim)
	g.POST("/api/page-review-tasks/:id/complete", handler.Complete)
	g.POST("/api/page-review-tasks/:id/fail", handler.Fail)
}
