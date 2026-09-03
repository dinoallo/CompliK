//nolint:testpackage // Tests exercise unexported normalization helpers.
package pagereviewtask

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeTaskRepository struct {
	enqueued *PageReviewTask
}

func (f *fakeTaskRepository) Enqueue(
	_ context.Context,
	task *PageReviewTask,
	_ time.Time,
) (*PageReviewTask, bool, error) {
	f.enqueued = task
	copiedTask := *task
	copiedTask.ID = 17
	return &copiedTask, true, nil
}

func (f *fakeTaskRepository) Claim(
	context.Context,
	string,
	int,
	time.Duration,
	time.Time,
) ([]PageReviewTask, error) {
	return nil, nil
}

func (f *fakeTaskRepository) Complete(
	context.Context,
	uint64,
	string,
	time.Time,
	time.Duration,
) error {
	return nil
}

func (f *fakeTaskRepository) Fail(context.Context, uint64, string, string, bool, time.Time) error {
	return nil
}

func TestNormalizeTaskCanonicalizesRouteAndURL(t *testing.T) {
	observedAt := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)

	task, err := normalizeTask(EnqueueItem{
		Namespace:   " ns-demo ",
		IngressName: " web ",
		Host:        "Example.COM",
		Path:        "//docs///?from=home",
		URL:         "HTTPS://example.com//docs/",
		ObservedAt:  observedAt,
	}, observedAt)
	if err != nil {
		t.Fatalf("normalizeTask() error = %v", err)
	}

	if task.Host != "example.com" || task.Path != "/docs" {
		t.Fatalf("unexpected canonical route: host=%q path=%q", task.Host, task.Path)
	}

	if task.URL != "https://example.com/docs" {
		t.Fatalf("unexpected canonical URL: %q", task.URL)
	}

	if task.Status != StatusPending || task.MaxAttempts != 3 || task.NextRunAt == nil {
		t.Fatalf("unexpected task state: %#v", task)
	}
}

func TestNormalizeTaskRejectsURLOutsideRouteHost(t *testing.T) {
	_, err := normalizeTask(EnqueueItem{
		Namespace:   "ns-demo",
		IngressName: "web",
		Host:        "example.com",
		Path:        "/docs",
		URL:         "https://other.example/docs",
	}, time.Now().UTC())
	if !errors.Is(err, ErrPageReviewTaskInvalidInput) {
		t.Fatalf("normalizeTask() error = %v, want %v", err, ErrPageReviewTaskInvalidInput)
	}
}

func TestServiceEnqueueBuildsStableTaskKey(t *testing.T) {
	repository := &fakeTaskRepository{}
	service := NewService(repository)

	results, err := service.Enqueue(context.Background(), EnqueueRequest{Items: []EnqueueItem{
		{
			Namespace:   "ns-demo",
			IngressName: "web",
			Host:        "example.com",
			Path:        "/docs/",
		},
	}})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if len(results) != 1 || !results[0].Queued {
		t.Fatalf("unexpected enqueue result: %#v", results)
	}

	if repository.enqueued.URL != "http://example.com/docs" {
		t.Fatalf("unexpected derived URL: %q", repository.enqueued.URL)
	}

	if len(repository.enqueued.TaskKey) != 64 {
		t.Fatalf("unexpected task key: %q", repository.enqueued.TaskKey)
	}
}
