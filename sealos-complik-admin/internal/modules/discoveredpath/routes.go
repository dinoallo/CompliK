package discoveredpath

import (
	"github.com/gin-gonic/gin"
	"sealos-complik-admin/internal/infra/database"
)

func InitRoutes(g *gin.Engine) {
	repository := NewRepository(database.Get())
	service := NewService(repository)
	handler := NewHandler(service)

	g.POST("/api/discovered-paths", handler.UpsertPaths)
	g.POST("/api/discovered-paths/top", handler.GetTopPaths)
	g.GET("/api/discovered-routes", handler.ListRoutes)
	g.GET("/api/discovered-paths", handler.ListPaths)
	g.DELETE("/api/discovered-paths/id/:id", handler.DeletePathByID)
}
