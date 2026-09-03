package pagereviewtask

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

type PageReviewTask struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement"                                                                                               json:"id"`
	TaskKey        string     `gorm:"column:task_key;size:64;not null;uniqueIndex:uk_page_review_tasks_task_key"                                             json:"task_key"`
	RouteKey       string     `gorm:"column:route_key;size:2048;not null;index:idx_page_review_tasks_route_status,priority:1"                                json:"route_key"`
	Namespace      string     `gorm:"size:255;not null"                                                                                                      json:"namespace"`
	IngressName    string     `gorm:"column:ingress_name;size:255;not null"                                                                                  json:"ingress_name"`
	Host           string     `gorm:"size:255;not null"                                                                                                      json:"host"`
	Path           string     `gorm:"size:1024;not null"                                                                                                     json:"path"`
	URL            string     `gorm:"size:2048;not null"                                                                                                     json:"url"`
	ContentVersion string     `gorm:"column:content_version;size:255"                                                                                        json:"content_version,omitempty"`
	PolicyVersion  string     `gorm:"column:policy_version;size:255"                                                                                         json:"policy_version,omitempty"`
	ObservedAt     time.Time  `gorm:"column:observed_at;not null"                                                                                            json:"observed_at"`
	Status         string     `gorm:"size:32;not null;index:idx_page_review_tasks_route_status,priority:2;index:idx_page_review_tasks_status_run,priority:1" json:"status"`
	Attempts       int        `gorm:"not null;default:0"                                                                                                     json:"attempts"`
	MaxAttempts    int        `gorm:"column:max_attempts;not null;default:3"                                                                                 json:"max_attempts"`
	LeaseOwner     string     `gorm:"column:lease_owner;size:255"                                                                                            json:"lease_owner,omitempty"`
	LeaseUntil     *time.Time `gorm:"column:lease_until;index:idx_page_review_tasks_status_run,priority:3"                                                   json:"lease_until,omitempty"`
	NextRunAt      *time.Time `gorm:"column:next_run_at;index:idx_page_review_tasks_status_run,priority:2"                                                   json:"next_run_at,omitempty"`
	LastError      string     `gorm:"column:last_error;type:text"                                                                                            json:"last_error,omitempty"`
	CreatedAt      time.Time  `gorm:"autoCreateTime"                                                                                                         json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime"                                                                                                         json:"updated_at"`
}

func (PageReviewTask) TableName() string {
	return "page_review_tasks"
}

func AutoMigrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("page review task automigrate: database is nil")
	}

	if err := db.AutoMigrate(&PageReviewTask{}); err != nil {
		return fmt.Errorf("page review task automigrate: %w", err)
	}

	return nil
}