package pagereviewtask

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Enqueue(c *gin.Context) {
	var req EnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"message": "invalid request body", "error": err.Error()},
		)

		return
	}

	results, err := h.service.Enqueue(c.Request.Context(), req)
	if err != nil {
		h.respondWithServiceError(c, err, "failed to enqueue page review tasks")
		return
	}

	response := EnqueueResponse{Tasks: make([]TaskResponse, 0, len(results))}
	for _, result := range results {
		response.Accepted++
		if result.Queued {
			response.Queued++
		}

		response.Tasks = append(response.Tasks, toTaskResponse(result.Task))
	}

	c.JSON(http.StatusAccepted, response)
}

func (h *Handler) Claim(c *gin.Context) {
	var req ClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"message": "invalid request body", "error": err.Error()},
		)

		return
	}

	tasks, err := h.service.Claim(c.Request.Context(), req)
	if err != nil {
		h.respondWithServiceError(c, err, "failed to claim page review tasks")
		return
	}

	response := ClaimResponse{Tasks: make([]TaskResponse, 0, len(tasks))}
	for i := range tasks {
		response.Tasks = append(response.Tasks, toTaskResponse(&tasks[i]))
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) Complete(c *gin.Context) {
	id, err := parseTaskID(c.Param("id"))
	if err != nil {
		h.respondWithServiceError(c, err, "invalid page review task id")
		return
	}

	var req TaskLifecycleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"message": "invalid request body", "error": err.Error()},
		)

		return
	}

	if err := h.service.Complete(c.Request.Context(), id, req); err != nil {
		h.respondWithServiceError(c, err, "failed to complete page review task")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "page review task completed successfully"})
}

func (h *Handler) Fail(c *gin.Context) {
	id, err := parseTaskID(c.Param("id"))
	if err != nil {
		h.respondWithServiceError(c, err, "invalid page review task id")
		return
	}

	var req FailTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"message": "invalid request body", "error": err.Error()},
		)

		return
	}

	if err := h.service.Fail(c.Request.Context(), id, req); err != nil {
		h.respondWithServiceError(c, err, "failed to fail page review task")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "page review task failure recorded successfully"})
}

func (h *Handler) respondWithServiceError(c *gin.Context, err error, fallbackMessage string) {
	switch {
	case errors.Is(err, ErrPageReviewTaskInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	case errors.Is(err, ErrPageReviewTaskNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case errors.Is(err, ErrPageReviewTaskLeaseLost):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	default:
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"message": fallbackMessage, "error": err.Error()},
		)
	}
}

func parseTaskID(raw string) (uint64, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, ErrPageReviewTaskInvalidInput
	}

	return id, nil
}