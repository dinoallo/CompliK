package pagereview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bearslyricattack/CompliK/complik/pkg/constants"
	"github.com/bearslyricattack/CompliK/complik/pkg/eventbus"
	"github.com/bearslyricattack/CompliK/complik/pkg/models"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClaimUsesAdminTaskContract(t *testing.T) {
	var received claimRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != claimPath || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("expected basic auth header")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"tasks":[{"id":42,"namespace":"ns-demo","ingress_name":"web","host":"example.com","path":"/docs","url":"https://example.com/docs"}]}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	worker := &Plugin{client: client}
	if err := worker.loadConfig(`{"adminBaseURL":"http://admin.example","adminTimeoutSecond":2,"leaseDurationSecond":30,"reviewTimeoutSecond":20,"adminBasicAuthUsername":"user","adminBasicAuthPassword":"pass"}`); err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	tasks, err := worker.claim(context.Background(), "worker-1", defaultBatchSize)
	if err != nil {
		t.Fatalf("claim() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != 42 {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
	if received.WorkerID != "worker-1" || received.Limit != defaultBatchSize || received.LeaseDurationSecond != 30 {
		t.Fatalf("unexpected claim request: %#v", received)
	}
}

func TestDetectorEventSignalsPendingTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker := &Plugin{ctx: ctx, pending: make(map[uint64]chan struct{})}
	resultCh := make(chan struct{}, 1)
	worker.pending[42] = resultCh
	bus := eventbus.NewEventBus(1)
	subscribe := bus.Subscribe(constants.DetectorTopic)
	worker.wg.Add(1)
	go worker.consumeDetectorEvents(subscribe)

	bus.Publish(constants.DetectorTopic, eventbus.Event{Payload: &models.DetectorInfo{
		ReviewTaskID: "42",
	}})

	select {
	case <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("detector event did not signal pending task")
	}

	cancel()
	bus.Unsubscribe(constants.DetectorTopic, subscribe)
	worker.wg.Wait()
}
