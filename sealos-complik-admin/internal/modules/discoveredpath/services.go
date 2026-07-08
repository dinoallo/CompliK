package discoveredpath

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrDiscoveredPathInvalidInput  = errors.New("invalid discovered path request")
	ErrDiscoveredPathInvalidCursor = errors.New("invalid discovered path cursor")
	ErrDiscoveredPathNotFound      = errors.New("discovered path not found")
)

const (
	defaultTopLimit  = 10
	maxTopLimit      = 100
	defaultListLimit = 50
	maxListLimit     = 200
	maxPathLength    = 1024
	cursorTimeLayout = "2006-01-02T15:04:05.000Z"
)

type Service struct {
	repository *Repository
}

func NewService(repository *Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) UpsertPaths(
	ctx context.Context,
	req UpsertDiscoveredPathsRequest,
) error {
	if len(req.Items) == 0 {
		return ErrDiscoveredPathInvalidInput
	}

	paths := make([]DiscoveredPath, 0, len(req.Items))
	for _, item := range req.Items {
		normalized, err := normalizeUpsertItem(item)
		if err != nil {
			return err
		}

		paths = append(paths, *normalized)
	}

	if err := s.repository.UpsertPaths(ctx, paths); err != nil {
		return translateRepositoryError(err)
	}

	return nil
}

func (s *Service) GetTopPaths(
	ctx context.Context,
	req TopDiscoveredPathsRequest,
) ([]TopDiscoveredPathsResponse, error) {
	if len(req.Routes) == 0 {
		return nil, ErrDiscoveredPathInvalidInput
	}

	limit := normalizeTopLimit(req.Limit)
	responses := make([]TopDiscoveredPathsResponse, 0, len(req.Routes))

	for _, routeReq := range req.Routes {
		route, err := normalizeRoute(routeReq.Namespace, routeReq.IngressName, routeReq.Host)
		if err != nil {
			return nil, err
		}

		paths, err := s.repository.ListTopPaths(ctx, *route, limit)
		if err != nil {
			return nil, translateRepositoryError(err)
		}

		responses = append(responses, TopDiscoveredPathsResponse{
			Namespace:   route.Namespace,
			IngressName: route.IngressName,
			Host:        route.Host,
			Paths:       toTopPathResponses(paths),
		})
	}

	return responses, nil
}

func (s *Service) ListRoutes(
	ctx context.Context,
	req ListDiscoveredRoutesQueryRequest,
) (*ListDiscoveredRoutesResponse, error) {
	cursor, err := parseRouteCursor(req.Cursor)
	if err != nil {
		return nil, err
	}

	limit := normalizeListLimit(req.Limit)

	routes, err := s.repository.ListRoutes(ctx, listRoutesOptions{
		Cursor:      cursor,
		Limit:       limit,
		Keyword:     req.Keyword,
		Namespace:   strings.TrimSpace(req.Namespace),
		IngressName: strings.TrimSpace(req.IngressName),
		Host:        strings.TrimSpace(req.Host),
	})
	if err != nil {
		return nil, translateRepositoryError(err)
	}

	hasMore := len(routes) > limit
	if hasMore {
		routes = routes[:limit]
	}

	if routes == nil {
		routes = make([]DiscoveredRouteResponse, 0)
	}

	return &ListDiscoveredRoutesResponse{
		List:       routes,
		NextCursor: nextRouteCursor(routes, hasMore),
		HasMore:    hasMore,
	}, nil
}

func (s *Service) ListPaths(
	ctx context.Context,
	req ListDiscoveredPathsQueryRequest,
) (*ListDiscoveredPathsResponse, error) {
	cursor, err := parsePathCursor(req.Cursor)
	if err != nil {
		return nil, err
	}

	limit := normalizeListLimit(req.Limit)

	paths, err := s.repository.ListPaths(ctx, listPathsOptions{
		Cursor:      cursor,
		Limit:       limit,
		Keyword:     req.Keyword,
		Namespace:   strings.TrimSpace(req.Namespace),
		IngressName: strings.TrimSpace(req.IngressName),
		Host:        strings.TrimSpace(req.Host),
	})
	if err != nil {
		return nil, translateRepositoryError(err)
	}

	hasMore := len(paths) > limit
	if hasMore {
		paths = paths[:limit]
	}

	responses := make([]DiscoveredPathResponse, 0, len(paths))
	for i := range paths {
		responses = append(responses, *toPathResponse(&paths[i]))
	}

	return &ListDiscoveredPathsResponse{
		List:       responses,
		NextCursor: nextPathCursor(paths, hasMore),
		HasMore:    hasMore,
	}, nil
}

func (s *Service) DeletePathByID(ctx context.Context, id uint64) error {
	if id == 0 {
		return ErrDiscoveredPathInvalidInput
	}

	if err := s.repository.DeletePathByID(ctx, id); err != nil {
		return translateRepositoryError(err)
	}

	return nil
}

func normalizeUpsertItem(item UpsertDiscoveredPathItem) (*DiscoveredPath, error) {
	if item.Count == 0 || item.LastSeenAt.IsZero() {
		return nil, ErrDiscoveredPathInvalidInput
	}

	route, err := normalizeRoute(item.Namespace, item.IngressName, item.Host)
	if err != nil {
		return nil, err
	}

	path, err := normalizePath(item.Path)
	if err != nil {
		return nil, err
	}

	return &DiscoveredPath{
		Namespace:   route.Namespace,
		IngressName: route.IngressName,
		Host:        route.Host,
		Path:        path,
		RouteHash:   route.RouteHash,
		PathHash:    hashValues(path),
		Count:       item.Count,
		LastSeenAt:  item.LastSeenAt,
	}, nil
}

func normalizeRoute(namespace, ingressName, host string) (*routeKey, error) {
	normalized := routeKey{
		Namespace:   strings.TrimSpace(namespace),
		IngressName: strings.TrimSpace(ingressName),
		Host:        strings.ToLower(strings.TrimSpace(host)),
	}

	if normalized.Namespace == "" || normalized.IngressName == "" || normalized.Host == "" {
		return nil, ErrDiscoveredPathInvalidInput
	}

	normalized.RouteHash = hashValues(
		normalized.Namespace,
		normalized.IngressName,
		normalized.Host,
	)

	return &normalized, nil
}

func normalizePath(rawPath string) (string, error) {
	pathValue := strings.TrimSpace(rawPath)
	if pathValue == "" {
		return "/", nil
	}

	if parsed, err := url.Parse(pathValue); err == nil && parsed.IsAbs() {
		pathValue = parsed.EscapedPath()
	} else {
		pathValue = trimPathQueryAndFragment(pathValue)
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
		return "", ErrDiscoveredPathInvalidInput
	}

	return pathValue, nil
}

func trimPathQueryAndFragment(pathValue string) string {
	if before, _, found := strings.Cut(pathValue, "#"); found {
		pathValue = before
	}

	if before, _, found := strings.Cut(pathValue, "?"); found {
		pathValue = before
	}

	return pathValue
}

func collapseSlashes(value string) string {
	if value == "" {
		return value
	}

	var builder strings.Builder
	builder.Grow(len(value))

	lastWasSlash := false
	for _, char := range value {
		if char == '/' {
			if lastWasSlash {
				continue
			}

			lastWasSlash = true
		} else {
			lastWasSlash = false
		}

		builder.WriteRune(char)
	}

	return builder.String()
}

func hashValues(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeTopLimit(limit int) int {
	if limit <= 0 {
		return defaultTopLimit
	}

	if limit > maxTopLimit {
		return maxTopLimit
	}

	return limit
}

func normalizeListLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}

	if limit > maxListLimit {
		return maxListLimit
	}

	return limit
}

func parsePathCursor(raw string) (*pathListCursor, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	parts := strings.Split(trimmed, ",")
	if len(parts) != 3 {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	count, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || count == 0 {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	lastSeenAt, err := parseCursorTime(parts[1])
	if err != nil {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	id, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || id == 0 {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	return &pathListCursor{
		Count:      count,
		LastSeenAt: lastSeenAt,
		ID:         id,
	}, nil
}

func parseRouteCursor(raw string) (*routeListCursor, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	parts := strings.Split(trimmed, ",")
	if len(parts) != 4 {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	lastSeenAt, err := parseCursorTime(parts[0])
	if err != nil {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	cursor := routeListCursor{
		LastSeenAt:  lastSeenAt,
		Namespace:   strings.TrimSpace(parts[1]),
		IngressName: strings.TrimSpace(parts[2]),
		Host:        strings.ToLower(strings.TrimSpace(parts[3])),
	}
	if cursor.Namespace == "" || cursor.IngressName == "" || cursor.Host == "" {
		return nil, ErrDiscoveredPathInvalidCursor
	}

	return &cursor, nil
}

func parseCursorTime(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if parsed, err := time.Parse(cursorTimeLayout, trimmed); err == nil {
		return parsed, nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, err
	}

	return parsed, nil
}

func nextPathCursor(paths []DiscoveredPath, hasMore bool) string {
	if !hasMore || len(paths) == 0 {
		return ""
	}

	last := paths[len(paths)-1]

	return fmt.Sprintf(
		"%d,%s,%d",
		last.Count,
		formatCursorTime(last.LastSeenAt),
		last.ID,
	)
}

func nextRouteCursor(routes []DiscoveredRouteResponse, hasMore bool) string {
	if !hasMore || len(routes) == 0 {
		return ""
	}

	last := routes[len(routes)-1]

	return strings.Join([]string{
		formatCursorTime(last.LastSeenAt),
		last.Namespace,
		last.IngressName,
		last.Host,
	}, ",")
}

func formatCursorTime(value time.Time) string {
	return value.UTC().Format(cursorTimeLayout)
}

func toPathResponse(path *DiscoveredPath) *DiscoveredPathResponse {
	if path == nil {
		return nil
	}

	return &DiscoveredPathResponse{
		ID:                  path.ID,
		Namespace:           path.Namespace,
		IngressName:         path.IngressName,
		Host:                path.Host,
		Path:                path.Path,
		Count:               path.Count,
		LastSeenAt:          path.LastSeenAt,
		LastDetectedAt:      path.LastDetectedAt,
		LastDetectionStatus: path.LastDetectionStatus,
		CreatedAt:           path.CreatedAt,
		UpdatedAt:           path.UpdatedAt,
	}
}

func toTopPathResponses(paths []DiscoveredPath) []TopDiscoveredPathItemResponse {
	responses := make([]TopDiscoveredPathItemResponse, 0, len(paths))
	for i := range paths {
		responses = append(responses, TopDiscoveredPathItemResponse{
			Path:                paths[i].Path,
			Count:               paths[i].Count,
			LastSeenAt:          paths[i].LastSeenAt,
			LastDetectedAt:      paths[i].LastDetectedAt,
			LastDetectionStatus: paths[i].LastDetectionStatus,
		})
	}

	return responses
}

func translateRepositoryError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrDiscoveredPathNotFound
	}

	return err
}
