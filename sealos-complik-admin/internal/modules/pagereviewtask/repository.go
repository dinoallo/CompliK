package pagereviewtask

import (
	"context"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TaskRepository interface {
	Enqueue(ctx context.Context, task *PageReviewTask, now time.Time) (*PageReviewTask, bool, error)
	Claim(
		ctx context.Context,
		workerID string,
		limit int,
		leaseDuration time.Duration,
		now time.Time,
	) ([]PageReviewTask, error)
	Complete(
		ctx context.Context,
		id uint64,
		workerID string,
		now time.Time,
		cooldown time.Duration,
	) error
	Fail(
		ctx context.Context,
		id uint64,
		workerID string,
		message string,
		retryable bool,
		now time.Time,
	) error
}

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Enqueue(
	ctx context.Context,
	task *PageReviewTask,
	now time.Time,
) (*PageReviewTask, bool, error) {
	if task == nil {
		return nil, false, errors.New("page review task is nil")
	}

	var result PageReviewTask

	queued := false
	for range 2 {
		result = PageReviewTask{}
		queued = false

		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var existing PageReviewTask

			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("task_key = ?", task.TaskKey).
				First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(task).Error; err != nil {
					return err
				}

				result = *task
				queued = true

				return nil
			}

			if err != nil {
				return err
			}

			result = existing
			if existing.Status == StatusPending || existing.Status == StatusRunning {
				if err := tx.Model(&existing).Updates(map[string]any{
					"url":         task.URL,
					"observed_at": task.ObservedAt,
				}).Error; err != nil {
					return err
				}

				result.URL = task.URL
				result.ObservedAt = task.ObservedAt

				return nil
			}

			if existing.NextRunAt != nil && existing.NextRunAt.After(now) {
				return nil
			}

			nextRunAt := now
			if err := tx.Model(&existing).Updates(map[string]any{
				"url":         task.URL,
				"observed_at": task.ObservedAt,
				"status":      StatusPending,
				"attempts":    0,
				"lease_owner": "",
				"lease_until": nil,
				"next_run_at": nextRunAt,
				"last_error":  "",
			}).Error; err != nil {
				return err
			}

			result = existing
			result.URL = task.URL
			result.ObservedAt = task.ObservedAt
			result.Status = StatusPending
			result.Attempts = 0
			result.LeaseOwner = ""
			result.LeaseUntil = nil
			result.NextRunAt = &nextRunAt
			result.LastError = ""
			queued = true

			return nil
		})
		if err == nil {
			return &result, queued, nil
		}

		if !isDuplicateKeyError(err) {
			return nil, false, err
		}
	}

	return nil, false, errors.New("page review task enqueue conflicted with another writer")
}

func (r *Repository) Claim(
	ctx context.Context,
	workerID string,
	limit int,
	leaseDuration time.Duration,
	now time.Time,
) ([]PageReviewTask, error) {
	var tasks []PageReviewTask

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&PageReviewTask{}).
			Where("status = ? AND lease_until IS NOT NULL AND lease_until < ?", StatusRunning, now).
			Updates(map[string]any{
				"status":      StatusPending,
				"lease_owner": "",
				"lease_until": nil,
				"next_run_at": now,
			}).Error; err != nil {
			return err
		}

		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND (next_run_at IS NULL OR next_run_at <= ?)", StatusPending, now).
			Order("id ASC").
			Limit(limit).
			Find(&tasks).Error; err != nil {
			return err
		}

		leaseUntil := now.Add(leaseDuration)
		for i := range tasks {
			if err := tx.Model(&tasks[i]).Updates(map[string]any{
				"status":      StatusRunning,
				"attempts":    gorm.Expr("attempts + ?", 1),
				"lease_owner": workerID,
				"lease_until": leaseUntil,
			}).Error; err != nil {
				return err
			}

			tasks[i].Status = StatusRunning
			tasks[i].Attempts++
			tasks[i].LeaseOwner = workerID
			tasks[i].LeaseUntil = &leaseUntil
		}

		return nil
	})

	return tasks, err
}

func (r *Repository) Complete(
	ctx context.Context,
	id uint64,
	workerID string,
	now time.Time,
	cooldown time.Duration,
) error {
	nextRunAt := now.Add(cooldown)

	result := r.db.WithContext(ctx).Model(&PageReviewTask{}).
		Where("id = ? AND status = ? AND lease_owner = ?", id, StatusRunning, workerID).
		Updates(map[string]any{
			"status":      StatusSucceeded,
			"lease_owner": "",
			"lease_until": nil,
			"next_run_at": nextRunAt,
			"last_error":  "",
		})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected > 0 {
		return nil
	}

	var existing PageReviewTask

	if err := r.db.WithContext(ctx).First(&existing, id).Error; err != nil {
		return err
	}

	if existing.Status == StatusSucceeded {
		return nil
	}

	return ErrPageReviewTaskLeaseLost
}

func (r *Repository) Fail(
	ctx context.Context,
	id uint64,
	workerID string,
	message string,
	retryable bool,
	now time.Time,
) error {
	var existing PageReviewTask
	if err := r.db.WithContext(ctx).
		Where("id = ? AND status = ? AND lease_owner = ?", id, StatusRunning, workerID).
		First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var current PageReviewTask
			if currentErr := r.db.WithContext(ctx).First(&current, id).Error; currentErr != nil {
				return currentErr
			}

			if current.Status == StatusSucceeded || current.Status == StatusFailed {
				return nil
			}

			return ErrPageReviewTaskLeaseLost
		}

		return err
	}

	nextStatus := StatusFailed
	next := now.Add(defaultTaskCooldown)

	nextRunAt := &next
	if retryable && existing.Attempts < existing.MaxAttempts {
		nextStatus = StatusPending
		retryAt := now.Add(retryDelay(existing.Attempts))
		nextRunAt = &retryAt
	}

	result := r.db.WithContext(ctx).Model(&PageReviewTask{}).
		Where("id = ? AND status = ? AND lease_owner = ?", id, StatusRunning, workerID).
		Updates(map[string]any{
			"status":      nextStatus,
			"lease_owner": "",
			"lease_until": nil,
			"next_run_at": nextRunAt,
			"last_error":  truncateError(message),
		})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected > 0 {
		return nil
	}

	var current PageReviewTask

	if err := r.db.WithContext(ctx).First(&current, id).Error; err != nil {
		return err
	}

	if current.Status == StatusSucceeded || current.Status == StatusFailed {
		return nil
	}

	return ErrPageReviewTaskLeaseLost
}

func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}

	delay := time.Duration(attempts) * 10 * time.Second
	if delay > 10*time.Minute {
		return 10 * time.Minute
	}

	return delay
}

func truncateError(message string) string {
	if len(message) <= 4096 {
		return message
	}

	return message[:4096]
}

func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}