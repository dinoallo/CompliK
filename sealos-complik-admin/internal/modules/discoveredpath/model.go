package discoveredpath

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type DiscoveredPath struct {
	ID                  uint64     `gorm:"primaryKey"`
	Namespace           string     `gorm:"size:255;not null"`
	IngressName         string     `gorm:"column:ingress_name;size:255;not null"`
	Host                string     `gorm:"size:255;not null"`
	Path                string     `gorm:"size:1024;not null"`
	RouteHash           string     `gorm:"column:route_hash;type:char(64);not null"`
	PathHash            string     `gorm:"column:path_hash;type:char(64);not null"`
	Count               uint64     `gorm:"not null"`
	LastSeenAt          time.Time  `gorm:"column:last_seen_at;not null"`
	LastDetectedAt      *time.Time `gorm:"column:last_detected_at"`
	LastDetectionStatus string     `gorm:"column:last_detection_status;size:32"`
	CreatedAt           time.Time  `gorm:"autoCreateTime"`
	UpdatedAt           time.Time  `gorm:"autoUpdateTime"`
}

type discoveredPathIndex struct {
	name      string
	statement string
}

var discoveredPathIndexes = []discoveredPathIndex{
	{
		name: "uk_discovered_paths_route_path",
		statement: "CREATE UNIQUE INDEX uk_discovered_paths_route_path " +
			"ON discovered_paths (`route_hash`, `path_hash`)",
	},
	{
		name: "idx_discovered_paths_route_top",
		statement: "CREATE INDEX idx_discovered_paths_route_top " +
			"ON discovered_paths (`route_hash`, `count`, `last_seen_at`, `id`)",
	},
	{
		name: "idx_discovered_paths_list",
		statement: "CREATE INDEX idx_discovered_paths_list " +
			"ON discovered_paths (`updated_at`, `id`)",
	},
	{
		name: "idx_discovered_paths_namespace",
		statement: "CREATE INDEX idx_discovered_paths_namespace " +
			"ON discovered_paths (`namespace`, `updated_at`, `id`)",
	},
	{
		name: "idx_discovered_paths_host",
		statement: "CREATE INDEX idx_discovered_paths_host " +
			"ON discovered_paths (`host`, `updated_at`, `id`)",
	},
}

func (DiscoveredPath) TableName() string {
	return "discovered_paths"
}

func AutoMigrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("discovered path automigrate: database is nil")
	}

	if err := db.AutoMigrate(&DiscoveredPath{}); err != nil {
		return fmt.Errorf("discovered path automigrate: %w", err)
	}

	if err := ensureDiscoveredPathIndexes(db); err != nil {
		return err
	}

	return nil
}

func ensureDiscoveredPathIndexes(db *gorm.DB) error {
	for _, index := range discoveredPathIndexes {
		if db.Migrator().HasIndex(&DiscoveredPath{}, index.name) {
			continue
		}

		if err := db.Exec(index.statement).Error; err != nil {
			return fmt.Errorf("create discovered path index %s: %w", index.name, err)
		}
	}

	return nil
}
