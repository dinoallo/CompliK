// Copyright 2025 CompliK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package browser provides a web page collector that captures HTML content and screenshots
// using a headless browser. It supports concurrent browser instance management through a
// browser pool and handles various error conditions during page navigation and rendering.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bearslyricattack/CompliK/complik/pkg/logger"
	"github.com/bearslyricattack/CompliK/complik/pkg/models"
	"github.com/bearslyricattack/CompliK/complik/plugins/compliance/collector/browser/utils"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type contextKey string

const startTimeContextKey contextKey = "start_time"

const (
	DeviceProfileDesktop = "desktop"
	DeviceProfileMobile  = "mobile"
)

type DeviceProfile struct {
	Name              string
	UserAgent         string
	Width             int
	Height            int
	DeviceScaleFactor float64
	Mobile            bool
}

func DefaultDeviceProfiles() []string {
	return []string{DeviceProfileDesktop, DeviceProfileMobile}
}

func ResolveDeviceProfile(name string) (DeviceProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", DeviceProfileDesktop:
		return DeviceProfile{
			Name:              DeviceProfileDesktop,
			UserAgent:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/96.0.4664.110 Safari/537.36",
			Width:             1366,
			Height:            768,
			DeviceScaleFactor: 1,
			Mobile:            false,
		}, true
	case DeviceProfileMobile:
		return DeviceProfile{
			Name:              DeviceProfileMobile,
			UserAgent:         "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
			Width:             390,
			Height:            844,
			DeviceScaleFactor: 3,
			Mobile:            true,
		}, true
	default:
		return DeviceProfile{}, false
	}
}

type CollectorInfo struct {
	DiscoveryName string `json:"discovery_name"`
	CollectorName string `json:"collector_name"`

	Name      string `json:"name"`
	Namespace string `json:"namespace"`

	Host string   `json:"host"`
	Path []string `json:"path"`
	URL  string   `json:"url"`

	DeviceProfile string `json:"device_profile"`
	Viewport      string `json:"viewport"`

	HTML       string `json:"html"`
	IsEmpty    bool   `json:"is_empty"`
	Screenshot []byte `json:"screenshot"`
}

type Collector struct {
	log logger.Logger
}

func NewCollector() *Collector {
	return &Collector{
		log: logger.GetLogger().WithField("component", "browser_collector"),
	}
}

func (s *Collector) CollectorAndScreenshot(
	ctx context.Context,
	discovery models.DiscoveryInfo,
	browserPool *utils.BrowserPool,
	name string,
	duration time.Duration,
	profile DeviceProfile,
) (*models.CollectorInfo, error) {
	taskCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	viewport := profile.Viewport()

	if discovery.PodCount == 0 {
		return &models.CollectorInfo{
			DiscoveryName: discovery.DiscoveryName,
			CollectorName: name,
			ReviewTaskID:  discovery.ReviewTaskID,
			Name:          discovery.Name,
			Namespace:     discovery.Namespace,
			Host:          discovery.Host,
			Path:          discovery.Path,
			URL:           "",
			DeviceProfile: profile.Name,
			Viewport:      viewport,
			HTML:          "",
			Screenshot:    nil,
			IsEmpty:       true,
		}, nil
	}

	instance, err := browserPool.Get(taskCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get browser instance: %w", err)
	}
	defer browserPool.Put(instance)

	page, err := s.setupPage(taskCtx, instance, profile)
	if err != nil {
		return nil, err
	}

	if page == nil {
		return nil, errors.New("page object is nil")
	}
	defer func() {
		if page != nil {
			_ = page.Close()
		}
	}()

	url := s.formatURL(discovery)

	wait := page.EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Type == proto.NetworkResourceTypeDocument && (e.Response.URL == url) {
			if e.Response.Status == 502 || e.Response.Status == 503 || e.Response.Status == 504 ||
				e.Response.Status == 404 {
				s.log.Error("Detected error status code", logger.Fields{
					"status_code": e.Response.Status,
					"url":         url,
					"namespace":   discovery.Namespace,
					"name":        discovery.Name,
				})
				cancel()
			}
		}
	})
	defer wait()

	err = page.Navigate(url)
	if err != nil {
		return nil, fmt.Errorf("page navigation failed: %w", err)
	}

	if err := taskCtx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			if discovery.PodCount == 0 {
				return &models.CollectorInfo{
					DiscoveryName: discovery.DiscoveryName,
					CollectorName: name,
					ReviewTaskID:  discovery.ReviewTaskID,
					Name:          discovery.Name,
					Namespace:     discovery.Namespace,
					Host:          discovery.Host,
					Path:          discovery.Path,
					URL:           "",
					DeviceProfile: profile.Name,
					Viewport:      viewport,
					HTML:          "",
					Screenshot:    nil,
					IsEmpty:       true,
				}, nil
			}
		}

		return nil, err
	}

	if err := s.waitForPageLoad(taskCtx, page); err != nil {
		return nil, err
	}

	content, err := page.HTML()
	if err != nil {
		s.log.Warn("Failed to get page content", logger.Fields{
			"error":     err.Error(),
			"url":       url,
			"namespace": discovery.Namespace,
			"name":      discovery.Name,
		})

		content = ""
	}

	if s.isErrorPage(content) {
		cancel()

		return &models.CollectorInfo{
			DiscoveryName: discovery.DiscoveryName,
			CollectorName: name,
			ReviewTaskID:  discovery.ReviewTaskID,
			Name:          discovery.Name,
			Namespace:     discovery.Namespace,
			Host:          discovery.Host,
			Path:          discovery.Path,
			URL:           "",
			DeviceProfile: profile.Name,
			Viewport:      viewport,
			HTML:          "",
			Screenshot:    nil,
			IsEmpty:       true,
		}, nil
	}

	screenshot, err := s.takeScreenshot(taskCtx, page)
	if err != nil {
		return nil, err
	}

	if startTime, ok := taskCtx.Value(startTimeContextKey).(time.Time); ok {
		duration = time.Duration(time.Since(startTime).Milliseconds())
	} else {
		duration = 0
	}

	s.log.Debug("Collection completed", logger.Fields{
		"url":             url,
		"html_length":     len(content),
		"screenshot_size": len(screenshot),
		"namespace":       discovery.Namespace,
		"name":            discovery.Name,
		"device_profile":  profile.Name,
		"viewport":        viewport,
		"duration_ms":     duration,
	})

	return &models.CollectorInfo{
		DiscoveryName: discovery.DiscoveryName,
		CollectorName: name,
		ReviewTaskID:  discovery.ReviewTaskID,
		Name:          discovery.Name,
		Namespace:     discovery.Namespace,
		Host:          discovery.Host,
		Path:          discovery.Path,
		URL:           url,
		DeviceProfile: profile.Name,
		Viewport:      viewport,
		HTML:          content,
		Screenshot:    screenshot,
		IsEmpty:       false,
	}, nil
}

func (s *Collector) formatURL(ingress models.DiscoveryInfo) string {
	if strings.TrimSpace(ingress.URL) != "" {
		return strings.TrimSpace(ingress.URL)
	}

	host := ingress.Host
	if host == "" {
		return ""
	}

	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return joinURLPath(host, ingress.Path)
	}

	return joinURLPath("http://"+host, ingress.Path)
}

func (s *Collector) setupPage(
	ctx context.Context,
	instance *utils.BrowserInstance,
	profile DeviceProfile,
) (*rod.Page, error) {
	var page *rod.Page

	if instance == nil {
		return nil, errors.New("browser instance is nil")
	}

	if instance.Browser == nil {
		return nil, errors.New("browser object is nil")
	}

	err := rod.Try(func() {
		page = instance.Browser.MustPage().Context(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create page: %w", err)
	}

	err = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: profile.UserAgent,
	})
	if err != nil {
		s.log.Error("Failed to set user agent", logger.Fields{
			"error": err.Error(),
		})
		return nil, err
	}

	err = page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:             profile.Width,
		Height:            profile.Height,
		DeviceScaleFactor: profile.DeviceScaleFactor,
		Mobile:            profile.Mobile,
	})
	if err != nil {
		s.log.Error("Failed to set viewport", logger.Fields{
			"error":  err.Error(),
			"width":  profile.Width,
			"height": profile.Height,
		})

		return nil, err
	}

	return page, nil
}

func (p DeviceProfile) Viewport() string {
	return fmt.Sprintf("%dx%d@%.2f", p.Width, p.Height, p.DeviceScaleFactor)
}

func joinURLPath(baseURL string, paths []string) string {
	if len(paths) == 0 || strings.TrimSpace(paths[0]) == "" || strings.TrimSpace(paths[0]) == "/" {
		return baseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/") +
			"/" +
			strings.TrimLeft(strings.TrimSpace(paths[0]), "/")
	}

	pathValue := strings.TrimSpace(paths[0])
	if strings.HasPrefix(pathValue, "/") {
		parsed.Path = pathValue
	} else {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + pathValue
	}

	return parsed.String()
}

func (s *Collector) waitForPageLoad(ctx context.Context, page *rod.Page) error {
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- page.WaitLoad()
	}()

	select {
	case err := <-waitDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Collector) isErrorPage(content string) bool {
	if len(content) >= 400 {
		return false
	}

	errorKeywords := []string{
		"upstream connect error",
		"no healthy upstream",
		"404 page not found",
		"403 Forbidden",
		"405 Method Not Allowed",
		"Not Found",
		"Function Not Found",
		"not found",
	}

	contentLower := strings.ToLower(content)
	for _, keyword := range errorKeywords {
		if strings.Contains(contentLower, strings.ToLower(keyword)) {
			return true
		}
	}

	return false
}

func (s *Collector) takeScreenshot(ctx context.Context, page *rod.Page) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var (
		screenshot []byte
		err        error
	)

	if rodErr := rod.Try(func() {
		screenshot, err = page.Context(ctx).Screenshot(true, &proto.PageCaptureScreenshot{
			Format:  proto.PageCaptureScreenshotFormatJpeg,
			Quality: &[]int{75}[0],
		})
	}); rodErr != nil {
		s.log.Error("Critical error during screenshot", logger.Fields{
			"error": rodErr.Error(),
		})
		return nil, rodErr
	}

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		s.log.Error("Screenshot failed", logger.Fields{
			"error": err.Error(),
		})

		return nil, err
	}

	return screenshot, nil
}
