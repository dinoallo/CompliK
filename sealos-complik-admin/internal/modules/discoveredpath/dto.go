package discoveredpath

import "time"

type DiscoveredPathIDRequest struct {
	ID uint64 `uri:"id" binding:"required,min=1"`
}

type UpsertDiscoveredPathsRequest struct {
	Items []UpsertDiscoveredPathItem `json:"items" binding:"required"`
}

type UpsertDiscoveredPathItem struct {
	Namespace   string    `json:"namespace"    binding:"required,max=255"`
	IngressName string    `json:"ingress_name" binding:"required,max=255"`
	Host        string    `json:"host"         binding:"required,max=255"`
	Path        string    `json:"path"         binding:"max=2048"`
	Count       uint64    `json:"count"        binding:"required,min=1"`
	LastSeenAt  time.Time `json:"last_seen_at" binding:"required"`
}

type TopDiscoveredPathsRequest struct {
	Routes []DiscoveredRouteRequest `json:"routes" binding:"required"`
	Limit  int                      `json:"limit"  binding:"omitempty,min=1,max=100"`
}

type DiscoveredRouteRequest struct {
	Namespace   string `json:"namespace"    binding:"required,max=255"`
	IngressName string `json:"ingress_name" binding:"required,max=255"`
	Host        string `json:"host"         binding:"required,max=255"`
}

type ListDiscoveredRoutesQueryRequest struct {
	Cursor      string `form:"cursor"`
	Limit       int    `form:"limit"        binding:"omitempty,min=1,max=200"`
	Keyword     string `form:"keyword"`
	Namespace   string `form:"namespace"    binding:"omitempty,max=255"`
	IngressName string `form:"ingress_name" binding:"omitempty,max=255"`
	Host        string `form:"host"         binding:"omitempty,max=255"`
}

type ListDiscoveredPathsQueryRequest struct {
	Cursor      string `form:"cursor"`
	Limit       int    `form:"limit"        binding:"omitempty,min=1,max=200"`
	Keyword     string `form:"keyword"`
	Namespace   string `form:"namespace"    binding:"omitempty,max=255"`
	IngressName string `form:"ingress_name" binding:"omitempty,max=255"`
	Host        string `form:"host"         binding:"omitempty,max=255"`
}

type DiscoveredPathResponse struct {
	ID                  uint64     `json:"id"`
	Namespace           string     `json:"namespace"`
	IngressName         string     `json:"ingress_name"`
	Host                string     `json:"host"`
	Path                string     `json:"path"`
	Count               uint64     `json:"count"`
	LastSeenAt          time.Time  `json:"last_seen_at"`
	LastDetectedAt      *time.Time `json:"last_detected_at,omitempty"`
	LastDetectionStatus string     `json:"last_detection_status,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type TopDiscoveredPathItemResponse struct {
	Path                string     `json:"path"`
	Count               uint64     `json:"count"`
	LastSeenAt          time.Time  `json:"last_seen_at"`
	LastDetectedAt      *time.Time `json:"last_detected_at,omitempty"`
	LastDetectionStatus string     `json:"last_detection_status,omitempty"`
}

type TopDiscoveredPathsResponse struct {
	Namespace   string                          `json:"namespace"`
	IngressName string                          `json:"ingress_name"`
	Host        string                          `json:"host"`
	Paths       []TopDiscoveredPathItemResponse `json:"paths"`
}

type DiscoveredRouteResponse struct {
	Namespace      string     `json:"namespace"`
	IngressName    string     `json:"ingress_name"`
	Host           string     `json:"host"`
	PathCount      uint64     `json:"path_count"`
	TotalCount     uint64     `json:"total_count"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	LastDetectedAt *time.Time `json:"last_detected_at,omitempty"`
}

type ListDiscoveredRoutesResponse struct {
	List       []DiscoveredRouteResponse `json:"list"`
	NextCursor string                    `json:"next_cursor,omitempty"`
	HasMore    bool                      `json:"has_more"`
}

type ListDiscoveredPathsResponse struct {
	List       []DiscoveredPathResponse `json:"list"`
	NextCursor string                   `json:"next_cursor,omitempty"`
	HasMore    bool                     `json:"has_more"`
}
