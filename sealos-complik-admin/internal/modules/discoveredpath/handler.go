package discoveredpath

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) UpsertPaths(c *gin.Context) {
	var req UpsertDiscoveredPathsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "invalid request body",
			"error":   err.Error(),
		})

		return
	}

	if err := h.service.UpsertPaths(c.Request.Context(), req); err != nil {
		h.respondWithServiceError(c, err, "failed to upsert discovered paths")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "discovered paths upserted successfully",
	})
}

func (h *Handler) GetTopPaths(c *gin.Context) {
	var req TopDiscoveredPathsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "invalid request body",
			"error":   err.Error(),
		})

		return
	}

	resp, err := h.service.GetTopPaths(c.Request.Context(), req)
	if err != nil {
		h.respondWithServiceError(c, err, "failed to get discovered top paths")
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) ListRoutes(c *gin.Context) {
	var req ListDiscoveredRoutesQueryRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "invalid request query",
			"error":   err.Error(),
		})

		return
	}

	resp, err := h.service.ListRoutes(c.Request.Context(), req)
	if err != nil {
		h.respondWithServiceError(c, err, "failed to list discovered routes")
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) ListPaths(c *gin.Context) {
	var req ListDiscoveredPathsQueryRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "invalid request query",
			"error":   err.Error(),
		})

		return
	}

	resp, err := h.service.ListPaths(c.Request.Context(), req)
	if err != nil {
		h.respondWithServiceError(c, err, "failed to list discovered paths")
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) DeletePathByID(c *gin.Context) {
	var req DiscoveredPathIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "invalid request path",
			"error":   err.Error(),
		})

		return
	}

	if err := h.service.DeletePathByID(c.Request.Context(), req.ID); err != nil {
		h.respondWithServiceError(c, err, "failed to delete discovered path")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "discovered path deleted successfully",
	})
}

func (h *Handler) respondWithServiceError(c *gin.Context, err error, fallbackMessage string) {
	switch {
	case errors.Is(err, ErrDiscoveredPathInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{
			"message": err.Error(),
		})
	case errors.Is(err, ErrDiscoveredPathInvalidCursor):
		c.JSON(http.StatusBadRequest, gin.H{
			"message": err.Error(),
		})
	case errors.Is(err, ErrDiscoveredPathNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"message": err.Error(),
		})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fallbackMessage,
			"error":   err.Error(),
		})
	}
}
