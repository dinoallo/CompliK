package discoveredpath

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

type routeKey struct {
	Namespace   string
	IngressName string
	Host        string
	RouteHash   string
}

type pathListCursor struct {
	Count      uint64
	LastSeenAt time.Time
	ID         uint64
}

type routeListCursor struct {
	LastSeenAt  time.Time
	Namespace   string
	IngressName string
	Host        string
}

type listPathsOptions struct {
	Cursor      *pathListCursor
	Limit       int
	Keyword     string
	Namespace   string
	IngressName string
	Host        string
}

type listRoutesOptions struct {
	Cursor      *routeListCursor
	Limit       int
	Keyword     string
	Namespace   string
	IngressName string
	Host        string
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) UpsertPaths(ctx context.Context, paths []DiscoveredPath) error {
	if len(paths) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "route_hash"},
			{Name: "path_hash"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"namespace":    gorm.Expr("VALUES(`namespace`)"),
			"ingress_name": gorm.Expr("VALUES(`ingress_name`)"),
			"host":         gorm.Expr("VALUES(`host`)"),
			"path":         gorm.Expr("VALUES(`path`)"),
			"count":        gorm.Expr("`count` + VALUES(`count`)"),
			"last_seen_at": gorm.Expr("GREATEST(`last_seen_at`, VALUES(`last_seen_at`))"),
			"updated_at":   gorm.Expr("VALUES(`updated_at`)"),
		}),
	}).Create(&paths).Error
}

func (r *Repository) ListTopPaths(
	ctx context.Context,
	route routeKey,
	limit int,
) ([]DiscoveredPath, error) {
	var paths []DiscoveredPath

	err := r.db.WithContext(ctx).
		Where("route_hash = ?", route.RouteHash).
		Where("namespace = ? AND ingress_name = ? AND host = ?",
			route.Namespace,
			route.IngressName,
			route.Host,
		).
		Order("`count` DESC, last_seen_at DESC, id DESC").
		Limit(limit).
		Find(&paths).Error
	if err != nil {
		return nil, err
	}

	return paths, nil
}

func (r *Repository) ListPaths(
	ctx context.Context,
	options listPathsOptions,
) ([]DiscoveredPath, error) {
	var paths []DiscoveredPath

	query := r.buildPathListQuery(ctx, options)
	if options.Cursor != nil {
		query = query.Where(
			"`count` < ? OR (`count` = ? AND last_seen_at < ?) OR (`count` = ? AND last_seen_at = ? AND id < ?)",
			options.Cursor.Count,
			options.Cursor.Count,
			options.Cursor.LastSeenAt,
			options.Cursor.Count,
			options.Cursor.LastSeenAt,
			options.Cursor.ID,
		)
	}

	if err := query.
		Order("`count` DESC, last_seen_at DESC, id DESC").
		Limit(options.Limit + 1).
		Find(&paths).Error; err != nil {
		return nil, err
	}

	return paths, nil
}

func (r *Repository) DeletePathByID(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&DiscoveredPath{})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

func (r *Repository) ListRoutes(
	ctx context.Context,
	options listRoutesOptions,
) ([]DiscoveredRouteResponse, error) {
	var routes []DiscoveredRouteResponse

	query := r.buildRouteListQuery(ctx, options)
	if options.Cursor != nil {
		query = query.Having(
			`MAX(last_seen_at) < ?
OR (
	MAX(last_seen_at) = ?
	AND (
		namespace > ?
		OR (namespace = ? AND ingress_name > ?)
		OR (namespace = ? AND ingress_name = ? AND host > ?)
	)
)`,
			options.Cursor.LastSeenAt,
			options.Cursor.LastSeenAt,
			options.Cursor.Namespace,
			options.Cursor.Namespace,
			options.Cursor.IngressName,
			options.Cursor.Namespace,
			options.Cursor.IngressName,
			options.Cursor.Host,
		)
	}

	if err := query.
		Order("last_seen_at DESC, namespace ASC, ingress_name ASC, host ASC").
		Limit(options.Limit + 1).
		Scan(&routes).Error; err != nil {
		return nil, err
	}

	return routes, nil
}

func (r *Repository) buildPathListQuery(
	ctx context.Context,
	options listPathsOptions,
) *gorm.DB {
	query := r.db.WithContext(ctx).Model(&DiscoveredPath{})

	if strings.TrimSpace(options.Keyword) != "" {
		value := "%" + strings.ToLower(strings.TrimSpace(options.Keyword)) + "%"
		query = query.Where(
			r.db.Where("LOWER(namespace) LIKE ?", value).
				Or("LOWER(ingress_name) LIKE ?", value).
				Or("LOWER(host) LIKE ?", value).
				Or("LOWER(path) LIKE ?", value),
		)
	}

	if strings.TrimSpace(options.Namespace) != "" {
		query = query.Where("namespace = ?", strings.TrimSpace(options.Namespace))
	}

	if strings.TrimSpace(options.IngressName) != "" {
		query = query.Where("ingress_name = ?", strings.TrimSpace(options.IngressName))
	}

	if strings.TrimSpace(options.Host) != "" {
		query = query.Where("host = ?", strings.ToLower(strings.TrimSpace(options.Host)))
	}

	return query
}

func (r *Repository) buildRouteListQuery(
	ctx context.Context,
	options listRoutesOptions,
) *gorm.DB {
	query := r.db.WithContext(ctx).
		Model(&DiscoveredPath{}).
		Select(
			"namespace,\n" +
				"ingress_name,\n" +
				"host,\n" +
				"COUNT(*) AS path_count,\n" +
				"COALESCE(SUM(`count`), 0) AS total_count,\n" +
				"MAX(last_seen_at) AS last_seen_at,\n" +
				"MAX(last_detected_at) AS last_detected_at",
		).
		Group("namespace, ingress_name, host")

	if strings.TrimSpace(options.Keyword) != "" {
		value := "%" + strings.ToLower(strings.TrimSpace(options.Keyword)) + "%"
		query = query.Where(
			r.db.Where("LOWER(namespace) LIKE ?", value).
				Or("LOWER(ingress_name) LIKE ?", value).
				Or("LOWER(host) LIKE ?", value),
		)
	}

	if strings.TrimSpace(options.Namespace) != "" {
		query = query.Where("namespace = ?", strings.TrimSpace(options.Namespace))
	}

	if strings.TrimSpace(options.IngressName) != "" {
		query = query.Where("ingress_name = ?", strings.TrimSpace(options.IngressName))
	}

	if strings.TrimSpace(options.Host) != "" {
		query = query.Where("host = ?", strings.ToLower(strings.TrimSpace(options.Host)))
	}

	return query
}
