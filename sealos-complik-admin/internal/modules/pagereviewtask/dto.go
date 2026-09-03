package pagereviewtask

import "time"

type EnqueueRequest struct {
	Items []EnqueueItem `json:"items" binding:"required"`
}

type EnqueueItem struct {
	Namespace      string    `json:"namespace"       binding:"required,max=255"`
	IngressName    string    `json:"ingress_name"    binding:"required,max=255"`
	Host           string    `json:"host"            binding:"required,max=255"`
	Path           string    `json:"path"            binding:"max=2048"`
	URL            string    `json:"url"             binding:"max=2048"`
	ObservedAt     time.Time `json:"observed_at"`
	ContentVersion string    `json:"content_version" binding:"max=255"`
	PolicyVersion  string    `json:"policy_version"  binding:"max=255"`
}

type EnqueueResponse struct {
	Accepted int            `json:"accepted"`
	Queued   int            `json:"queued"`
	Tasks    []TaskResponse `json:"tasks"`
}

type ClaimRequest struct {
	WorkerID            string `json:"worker_id"             binding:"required,max=255"`
	Limit               int    `json:"limit"                 binding:"omitempty,min=1,max=100"`
	LeaseDurationSecond int    `json:"lease_duration_second" binding:"omitempty,min=10,max=3600"`
}

type ClaimResponse struct {
	Tasks []TaskResponse `json:"tasks"`
}

type TaskLifecycleRequest struct {
	WorkerID string `json:"worker_id" binding:"required,max=255"`
}

type FailTaskRequest struct {
	WorkerID  string `json:"worker_id" binding:"required,max=255"`
	Error     string `json:"error"     binding:"max=4096"`
	Retryable *bool  `json:"retryable"`
}

type TaskResponse struct {
	ID             uint64     `json:"id"`
	TaskKey        string     `json:"task_key"`
	Namespace      string     `json:"namespace"`
	IngressName    string     `json:"ingress_name"`
	Host           string     `json:"host"`
	Path           string     `json:"path"`
	URL            string     `json:"url"`
	ContentVersion string     `json:"content_version,omitempty"`
	PolicyVersion  string     `json:"policy_version,omitempty"`
	ObservedAt     time.Time  `json:"observed_at"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	LeaseUntil     *time.Time `json:"lease_until,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
}

func toTaskResponse(task *PageReviewTask) TaskResponse {
	if task == nil {
		return TaskResponse{}
	}

	return TaskResponse{
		ID:             task.ID,
		TaskKey:        task.TaskKey,
		Namespace:      task.Namespace,
		IngressName:    task.IngressName,
		Host:           task.Host,
		Path:           task.Path,
		URL:            task.URL,
		ContentVersion: task.ContentVersion,
		PolicyVersion:  task.PolicyVersion,
		ObservedAt:     task.ObservedAt,
		Status:         task.Status,
		Attempts:       task.Attempts,
		MaxAttempts:    task.MaxAttempts,
		LeaseUntil:     task.LeaseUntil,
		NextRunAt:      task.NextRunAt,
	}
}