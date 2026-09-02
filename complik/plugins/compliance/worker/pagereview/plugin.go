package pagereview

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bearslyricattack/CompliK/complik/pkg/constants"
	"github.com/bearslyricattack/CompliK/complik/pkg/eventbus"
	"github.com/bearslyricattack/CompliK/complik/pkg/logger"
	"github.com/bearslyricattack/CompliK/complik/pkg/models"
	"github.com/bearslyricattack/CompliK/complik/pkg/plugin"
	"github.com/bearslyricattack/CompliK/complik/pkg/utils/config"
)

const (
	pluginName = constants.CompliancePageReviewWorkerName
	pluginType = constants.ComplianceCollectorPluginType

	defaultPollInterval     = 5 * time.Second
	defaultReviewTimeout    = 180 * time.Second
	defaultLeaseDuration    = 240 * time.Second
	defaultInitialDelay     = 10 * time.Second
	defaultBatchSize        = 10
	defaultMaxWorkers       = 10
	maxBatchSize            = 100
	maxReviewTimeout        = 30 * time.Minute
	pageReviewDiscoveryName = "PageReview"
	claimPath               = "/api/page-review-tasks/claim"
)

func init() {
	plugin.PluginFactories[pluginName] = func() plugin.Plugin {
		return &Plugin{
			log: logger.GetLogger().WithField("plugin", pluginName),
		}
	}
}

type Plugin struct {
	log    logger.Logger
	config workerConfig
	client *http.Client

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	pending map[uint64]chan struct{}
}

type workerConfig struct {
	AdminBaseURL           string `json:"adminBaseURL"`
	AdminTimeoutSecond     int    `json:"adminTimeoutSecond"`
	PollIntervalSecond     int    `json:"pollIntervalSecond"`
	ReviewTimeoutSecond    int    `json:"reviewTimeoutSecond"`
	LeaseDurationSecond    int    `json:"leaseDurationSecond"`
	InitialDelaySecond     int    `json:"initialDelaySecond"`
	BatchSize              int    `json:"batchSize"`
	MaxWorkers             int    `json:"maxWorkers"`
	AdminBasicAuthUsername string `json:"adminBasicAuthUsername"`
	AdminBasicAuthPassword string `json:"adminBasicAuthPassword"`
}

type taskResponse struct {
	ID             uint64    `json:"id"`
	Namespace      string    `json:"namespace"`
	IngressName    string    `json:"ingress_name"`
	Host           string    `json:"host"`
	Path           string    `json:"path"`
	URL            string    `json:"url"`
	ContentVersion string    `json:"content_version,omitempty"`
	PolicyVersion  string    `json:"policy_version,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
}

type claimRequest struct {
	WorkerID            string `json:"worker_id"`
	Limit               int    `json:"limit"`
	LeaseDurationSecond int    `json:"lease_duration_second"`
}

type claimResponse struct {
	Tasks []taskResponse `json:"tasks"`
}

type lifecycleRequest struct {
	WorkerID string `json:"worker_id"`
}

type failRequest struct {
	WorkerID  string `json:"worker_id"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
}

func (p *Plugin) Name() string {
	return pluginName
}

func (p *Plugin) Type() string {
	return pluginType
}

func (p *Plugin) Start(
	ctx context.Context,
	cfg config.PluginConfig,
	eventBus *eventbus.EventBus,
) error {
	if err := p.loadConfig(cfg.Settings); err != nil {
		return err
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.client = &http.Client{Timeout: time.Duration(p.config.AdminTimeoutSecond) * time.Second}
	p.pending = make(map[uint64]chan struct{})
	workerID := newWorkerID()
	detectorEvents := eventBus.Subscribe(constants.DetectorTopic)

	p.wg.Add(1)
	go p.consumeDetectorEvents(detectorEvents)

	p.log.Info("Page review worker started", logger.Fields{
		"admin_base_url":       p.config.AdminBaseURL,
		"poll_interval_second": p.config.PollIntervalSecond,
		"batch_size":           p.config.BatchSize,
		"max_workers":          p.config.MaxWorkers,
	})

	defer func() {
		eventBus.Unsubscribe(constants.DetectorTopic, detectorEvents)
		p.wg.Wait()
	}()

	if p.config.InitialDelaySecond > 0 {
		select {
		case <-time.After(time.Duration(p.config.InitialDelaySecond) * time.Second):
		case <-p.ctx.Done():
			return nil
		}
	}

	semaphore := make(chan struct{}, p.config.MaxWorkers)
	poll := time.NewTicker(time.Duration(p.config.PollIntervalSecond) * time.Second)
	defer poll.Stop()

	for {
		if err := p.claimAndStart(p.ctx, workerID, semaphore, eventBus); err != nil {
			p.log.Warn("Failed to poll page review tasks", logger.Fields{"error": err.Error()})
		}

		select {
		case <-poll.C:
		case <-p.ctx.Done():
			return nil
		case <-ctx.Done():
			p.cancel()
			return nil
		}
	}
}

func (p *Plugin) Stop(ctx context.Context) error {
	if p.cancel == nil {
		return nil
	}

	p.cancel()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Plugin) loadConfig(setting string) error {
	p.config = workerConfig{
		AdminBaseURL:        config.DefaultAdminBaseURL,
		AdminTimeoutSecond:  config.DefaultAdminTimeoutSecond,
		PollIntervalSecond:  int(defaultPollInterval / time.Second),
		ReviewTimeoutSecond: int(defaultReviewTimeout / time.Second),
		LeaseDurationSecond: int(defaultLeaseDuration / time.Second),
		InitialDelaySecond:  int(defaultInitialDelay / time.Second),
		BatchSize:           defaultBatchSize,
		MaxWorkers:          defaultMaxWorkers,
	}

	if strings.TrimSpace(setting) == "" {
		p.applyAuth(workerConfig{})
		return nil
	}

	var parsed workerConfig
	if err := json.Unmarshal([]byte(setting), &parsed); err != nil {
		return fmt.Errorf("parse page review worker configuration: %w", err)
	}

	if strings.TrimSpace(parsed.AdminBaseURL) != "" {
		resolved, err := config.GetSecureValue(parsed.AdminBaseURL)
		if err != nil {
			return fmt.Errorf("resolve admin base url: %w", err)
		}
		p.config.AdminBaseURL = resolved
	}
	if parsed.AdminTimeoutSecond > 0 {
		p.config.AdminTimeoutSecond = parsed.AdminTimeoutSecond
	}
	if parsed.PollIntervalSecond > 0 {
		p.config.PollIntervalSecond = parsed.PollIntervalSecond
	}
	if parsed.ReviewTimeoutSecond > 0 {
		p.config.ReviewTimeoutSecond = parsed.ReviewTimeoutSecond
	}
	if parsed.LeaseDurationSecond > 0 {
		p.config.LeaseDurationSecond = parsed.LeaseDurationSecond
	}
	if parsed.InitialDelaySecond > 0 {
		p.config.InitialDelaySecond = parsed.InitialDelaySecond
	}
	if parsed.BatchSize > 0 {
		p.config.BatchSize = parsed.BatchSize
	}
	if parsed.MaxWorkers > 0 {
		p.config.MaxWorkers = parsed.MaxWorkers
	}

	if p.config.BatchSize > maxBatchSize || p.config.MaxWorkers > maxBatchSize ||
		p.config.PollIntervalSecond <= 0 || p.config.AdminTimeoutSecond <= 0 ||
		p.config.ReviewTimeoutSecond <= 0 || time.Duration(p.config.ReviewTimeoutSecond)*time.Second > maxReviewTimeout ||
		p.config.LeaseDurationSecond < p.config.ReviewTimeoutSecond {
		return errors.New("invalid page review worker limits or lease duration")
	}

	p.applyAuth(parsed)
	return nil
}

func (p *Plugin) applyAuth(parsed workerConfig) {
	auth := config.ResolveAdminBasicAuth(
		parsed.AdminBasicAuthUsername,
		parsed.AdminBasicAuthPassword,
	)
	p.config.AdminBasicAuthUsername = auth.Username
	p.config.AdminBasicAuthPassword = auth.Password
}

func (p *Plugin) claimAndStart(
	ctx context.Context,
	workerID string,
	semaphore chan struct{},
	eventBus *eventbus.EventBus,
) error {
	available := cap(semaphore) - len(semaphore)
	if available <= 0 {
		return nil
	}
	limit := p.config.BatchSize
	if limit > available {
		limit = available
	}

	tasks, err := p.claim(ctx, workerID, limit)
	if err != nil {
		return err
	}

	for _, task := range tasks {
		semaphore <- struct{}{}
		p.wg.Add(1)
		go func(task taskResponse) {
			defer p.wg.Done()
			defer func() { <-semaphore }()
			p.processTask(ctx, workerID, task, eventBus)
		}(task)
	}

	return nil
}

func (p *Plugin) processTask(
	ctx context.Context,
	workerID string,
	task taskResponse,
	eventBus *eventbus.EventBus,
) {
	result := make(chan struct{}, 1)
	p.mu.Lock()
	p.pending[task.ID] = result
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, task.ID)
		p.mu.Unlock()
	}()

	eventBus.Publish(constants.DiscoveryTopic, eventbus.Event{Payload: models.DiscoveryInfo{
		DiscoveryName: pageReviewDiscoveryName,
		ReviewTaskID:  strconv.FormatUint(task.ID, 10),
		Name:          task.IngressName,
		Namespace:     task.Namespace,
		Host:          task.Host,
		Path:          []string{task.Path},
		URL:           task.URL,
		HasActivePods: true,
		PodCount:      1,
	}})

	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(p.config.ReviewTimeoutSecond)*time.Second)
	defer cancel()

	select {
	case <-result:
		if err := p.complete(ctx, workerID, task.ID); err != nil {
			p.log.Error("Failed to complete page review task", logger.Fields{
				"task_id": task.ID,
				"error":   err.Error(),
			})
		}
	case <-taskCtx.Done():
		if err := p.fail(ctx, workerID, task.ID, "review timed out waiting for detector result", true); err != nil {
			p.log.Error("Failed to record page review timeout", logger.Fields{
				"task_id": task.ID,
				"error":   err.Error(),
			})
		}
	}
}

func (p *Plugin) consumeDetectorEvents(subscribe eventbus.EventChan) {
	defer p.wg.Done()
	for {
		select {
		case event, ok := <-subscribe:
			if !ok {
				return
			}

			result, ok := event.Payload.(*models.DetectorInfo)
			if !ok || result == nil || strings.TrimSpace(result.ReviewTaskID) == "" {
				continue
			}

			taskID, err := strconv.ParseUint(result.ReviewTaskID, 10, 64)
			if err != nil || taskID == 0 {
				p.log.Warn("Ignoring detector result with invalid review task id", logger.Fields{
					"review_task_id": result.ReviewTaskID,
				})
				continue
			}

			p.mu.Lock()
			resultCh := p.pending[taskID]
			p.mu.Unlock()
			if resultCh == nil {
				continue
			}

			select {
			case resultCh <- struct{}{}:
			default:
			}
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Plugin) claim(ctx context.Context, workerID string, limit int) ([]taskResponse, error) {
	request := claimRequest{
		WorkerID:            workerID,
		Limit:               limit,
		LeaseDurationSecond: p.config.LeaseDurationSecond,
	}

	var response claimResponse
	if err := p.postJSON(ctx, claimPath, request, &response); err != nil {
		return nil, err
	}

	return response.Tasks, nil
}

func (p *Plugin) complete(ctx context.Context, workerID string, taskID uint64) error {
	return p.postJSON(
		ctx,
		fmt.Sprintf("/api/page-review-tasks/%d/complete", taskID),
		lifecycleRequest{WorkerID: workerID},
		nil,
	)
}

func (p *Plugin) fail(ctx context.Context, workerID string, taskID uint64, message string, retryable bool) error {
	return p.postJSON(
		ctx,
		fmt.Sprintf("/api/page-review-tasks/%d/fail", taskID),
		failRequest{WorkerID: workerID, Error: message, Retryable: retryable},
		nil,
	)
}

func (p *Plugin) postJSON(ctx context.Context, path string, payload any, response any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal page review request: %w", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(p.config.AdminTimeoutSecond)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		strings.TrimRight(p.config.AdminBaseURL, "/")+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create page review request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	config.AdminBasicAuth{
		Username: p.config.AdminBasicAuthUsername,
		Password: p.config.AdminBasicAuthPassword,
	}.Apply(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send page review request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read page review response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("page review api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	if response != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, response); err != nil {
			return fmt.Errorf("decode page review response: %w", err)
		}
	}

	return nil
}

func newWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown"
	}

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("complik-page-review-%s-%d", hostname, time.Now().UnixNano())
	}

	return fmt.Sprintf("complik-page-review-%s-%s", hostname, hex.EncodeToString(suffix[:]))
}
