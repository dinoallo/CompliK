package pagereviewtask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrPageReviewTaskInvalidInput = errors.New("invalid page review task request")
	ErrPageReviewTaskNotFound     = errors.New("page review task not found")
	ErrPageReviewTaskLeaseLost    = errors.New("page review task lease is no longer valid")
)

const (
	defaultTaskCooldown = 10 * time.Minute
	defaultClaimLimit   = 20
	maxEnqueueItems     = 100
	maxClaimLimit       = 100
	maxPathLength       = 1024
)

type Service struct {
	repository TaskRepository
}

func NewService(repository TaskRepository) *Service {
	return &Service{repository: repository}
}

type EnqueueResult struct {
	Task   *PageReviewTask
	Queued bool
}

func (s *Service) Enqueue(
	ctx context.Context,
	req EnqueueRequest,
) ([]EnqueueResult, error) {
	if len(req.Items) == 0 || len(req.Items) > maxEnqueueItems {
		return nil, ErrPageReviewTaskInvalidInput
	}

	now := time.Now().UTC()

	results := make([]EnqueueResult, 0, len(req.Items))
	for _, item := range req.Items {
		task, err := normalizeTask(item, now)
		if err != nil {
			return nil, err
		}

		stored, queued, err := s.repository.Enqueue(ctx, task, now)
		if err != nil {
			return nil, translateRepositoryError(err)
		}

		results = append(results, EnqueueResult{Task: stored, Queued: queued})
	}

	return results, nil
}

func (s *Service) Claim(
	ctx context.Context,
	req ClaimRequest,
) ([]PageReviewTask, error) {
	workerID := strings.TrimSpace(req.WorkerID)
	if workerID == "" {
		return nil, ErrPageReviewTaskInvalidInput
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultClaimLimit
	}

	if limit > maxClaimLimit {
		return nil, ErrPageReviewTaskInvalidInput
	}

	leaseSecond := req.LeaseDurationSecond

	if leaseSecond <= 0 {
		leaseSecond = 180
	}

	if leaseSecond < 10 || leaseSecond > 3600 {
		return nil, ErrPageReviewTaskInvalidInput
	}

	tasks, err := s.repository.Claim(
		ctx,
		workerID,
		limit,
		time.Duration(leaseSecond)*time.Second,
		time.Now().UTC(),
	)
	if err != nil {
		return nil, translateRepositoryError(err)
	}

	return tasks, nil
}

func (s *Service) Complete(ctx context.Context, id uint64, req TaskLifecycleRequest) error {
	if id == 0 || strings.TrimSpace(req.WorkerID) == "" {
		return ErrPageReviewTaskInvalidInput
	}

	return translateRepositoryError(s.repository.Complete(
		ctx,
		id,
		strings.TrimSpace(req.WorkerID),
		time.Now().UTC(),
		defaultTaskCooldown,
	))
}

func (s *Service) Fail(ctx context.Context, id uint64, req FailTaskRequest) error {
	if id == 0 || strings.TrimSpace(req.WorkerID) == "" {
		return ErrPageReviewTaskInvalidInput
	}

	retryable := true
	if req.Retryable != nil {
		retryable = *req.Retryable
	}

	return translateRepositoryError(s.repository.Fail(
		ctx,
		id,
		strings.TrimSpace(req.WorkerID),
		strings.TrimSpace(req.Error),
		retryable,
		time.Now().UTC(),
	))
}

func normalizeTask(item EnqueueItem, now time.Time) (*PageReviewTask, error) {
	namespace := strings.TrimSpace(item.Namespace)
	ingressName := strings.TrimSpace(item.IngressName)

	host := normalizeHost(item.Host)
	if namespace == "" || ingressName == "" || host == "" || strings.ContainsAny(host, "/?#") {
		return nil, ErrPageReviewTaskInvalidInput
	}

	path, err := normalizePath(item.Path)
	if err != nil {
		return nil, err
	}

	pageURL, err := normalizeTaskURL(item.URL, host, path)
	if err != nil {
		return nil, err
	}

	observedAt := item.ObservedAt
	if observedAt.IsZero() {
		observedAt = now
	}

	contentVersion := strings.TrimSpace(item.ContentVersion)
	policyVersion := strings.TrimSpace(item.PolicyVersion)
	routeKey := strings.Join([]string{namespace, ingressName, host}, "/") + path
	taskKey := hashValues(
		namespace,
		ingressName,
		host,
		path,
		contentVersion,
		policyVersion,
	)
	nextRunAt := now

	return &PageReviewTask{
		TaskKey:        taskKey,
		RouteKey:       routeKey,
		Namespace:      namespace,
		IngressName:    ingressName,
		Host:           host,
		Path:           path,
		URL:            pageURL,
		ContentVersion: contentVersion,
		PolicyVersion:  policyVersion,
		ObservedAt:     observedAt,
		Status:         StatusPending,
		MaxAttempts:    3,
		NextRunAt:      &nextRunAt,
	}, nil
}

func normalizeHost(raw string) string {
	host := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		if parsed, err := url.Parse(host); err == nil {
			host = strings.ToLower(parsed.Host)
		}
	}

	return strings.TrimSuffix(host, ".")
}

func normalizePath(raw string) (string, error) {
	pathValue := strings.TrimSpace(raw)
	if pathValue == "" {
		return "/", nil
	}

	if parsed, err := url.Parse(pathValue); err == nil && parsed.IsAbs() {
		pathValue = parsed.EscapedPath()
	} else {
		if before, _, found := strings.Cut(pathValue, "#"); found {
			pathValue = before
		}

		if before, _, found := strings.Cut(pathValue, "?"); found {
			pathValue = before
		}
	}

	if decoded, err := url.PathUnescape(pathValue); err == nil {
		pathValue = decoded
	}

	pathValue = collapseSlashes(pathValue)
	if pathValue == "" {
		pathValue = "/"
	}

	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}

	if len(pathValue) > 1 {
		pathValue = strings.TrimRight(pathValue, "/")
	}

	if len(pathValue) > maxPathLength {
		return "", ErrPageReviewTaskInvalidInput
	}

	return pathValue, nil
}

func normalizeTaskURL(rawURL, host, path string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "http://" + host + path, nil
	}

	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrPageReviewTaskInvalidInput
	}

	if strings.ToLower(parsed.Host) != host {
		return "", ErrPageReviewTaskInvalidInput
	}

	parsedPath, err := normalizePath(parsed.EscapedPath())
	if err != nil || parsedPath != path {
		return "", ErrPageReviewTaskInvalidInput
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = host
	parsed.Path = path
	parsed.RawPath = ""

	return parsed.String(), nil
}

func collapseSlashes(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))

	lastSlash := false
	for _, char := range value {
		if char == '/' {
			if lastSlash {
				continue
			}

			lastSlash = true
		} else {
			lastSlash = false
		}

		builder.WriteRune(char)
	}

	return builder.String()
}

func hashValues(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func translateRepositoryError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrPageReviewTaskNotFound
	}

	return err
}
