package repository

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/util"
)

// ErrWaitlistDuplicate 同一人同一地块已存在有效候补（唯一索引兜底）。
var ErrWaitlistDuplicate = errors.New("duplicate active waitlist entry")

// IsDuplicateWaitlistErr 判断是否为候补唯一约束冲突（PostgreSQL / SQLite）。
func IsDuplicateWaitlistErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "uniq_waitlist_active") ||
		strings.Contains(msg, "UNIQUE constraint failed: waitlist_entries") ||
		strings.Contains(strings.ToUpper(msg), "DUPLICATE KEY")
}

// WaitlistRepository 地块候补仓储接口。
type WaitlistRepository interface {
	Create(tx *gorm.DB, e *model.WaitlistEntry) error
	Update(tx *gorm.DB, e *model.WaitlistEntry) error
	FindByID(id uint) (*model.WaitlistEntry, error)
	FindByIDForUpdate(tx *gorm.DB, id uint) (*model.WaitlistEntry, error)
	FindActive(plotID, userID uint) (*model.WaitlistEntry, error)
	FindActiveForUpdate(tx *gorm.DB, plotID, userID uint) (*model.WaitlistEntry, error)
	// HeadForUpdate 锁定并返回队首：优先 invited，其次最早登记的 waiting（同时间按 id）。
	HeadForUpdate(tx *gorm.DB, plotID uint) (*model.WaitlistEntry, error)
	ListActiveByPlotForUpdate(tx *gorm.DB, plotID uint) ([]model.WaitlistEntry, error)
	ListByPlot(plotID uint, status string, pq util.PageQuery) ([]model.WaitlistEntry, int64, error)
	ListByUser(userID uint, status string, pq util.PageQuery) ([]model.WaitlistEntry, int64, error)
	// ListAll 管理端全量队列（可按地块/状态过滤）。
	ListAll(plotID uint, status string, pq util.PageQuery) ([]model.WaitlistEntry, int64, error)
	// CountAhead 有效队列中排在 entry 之前的人数（registered_at, id 排序）。
	CountAhead(tx *gorm.DB, entry *model.WaitlistEntry) (int64, error)
	CountActiveByPlot(plotID uint) (int64, error)
	// ListExpiredInvited 列出已过确认截止时间仍处于 invited 的记录（后台扫描）。
	ListExpiredInvited(now time.Time, limit int) ([]model.WaitlistEntry, error)
	// ListInvitedPlotIDs 存在有效 invited 记录的地块 ID 集合。
	ListInvitedPlotIDs() ([]uint, error)
}

type waitlistRepository struct {
	db *gorm.DB
}

// NewWaitlistRepository 构造候补仓储。
func NewWaitlistRepository(db *gorm.DB) WaitlistRepository {
	return &waitlistRepository{db: db}
}

func waitlistActiveStatuses() []string {
	out := make([]string, 0, len(constants.WaitlistActiveStatuses))
	for _, s := range constants.WaitlistActiveStatuses {
		out = append(out, string(s))
	}
	return out
}

func (r *waitlistRepository) Create(tx *gorm.DB, e *model.WaitlistEntry) error {
	db := tx
	if db == nil {
		db = r.db
	}
	if err := db.Create(e).Error; err != nil {
		if IsDuplicateWaitlistErr(err) {
			return ErrWaitlistDuplicate
		}
		return err
	}
	return nil
}

func (r *waitlistRepository) Update(tx *gorm.DB, e *model.WaitlistEntry) error {
	db := tx
	if db == nil {
		db = r.db
	}
	if err := db.Save(e).Error; err != nil {
		if IsDuplicateWaitlistErr(err) {
			return ErrWaitlistDuplicate
		}
		return err
	}
	return nil
}

func (r *waitlistRepository) FindByID(id uint) (*model.WaitlistEntry, error) {
	var e model.WaitlistEntry
	if err := r.db.Preload("User").Preload("Plot").First(&e, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *waitlistRepository) FindByIDForUpdate(tx *gorm.DB, id uint) (*model.WaitlistEntry, error) {
	var e model.WaitlistEntry
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("User").Preload("Plot").First(&e, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *waitlistRepository) FindActive(plotID, userID uint) (*model.WaitlistEntry, error) {
	var e model.WaitlistEntry
	err := r.db.Where("plot_id = ? AND user_id = ? AND status IN ?", plotID, userID, waitlistActiveStatuses()).
		Preload("Plot").First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *waitlistRepository) FindActiveForUpdate(tx *gorm.DB, plotID, userID uint) (*model.WaitlistEntry, error) {
	var e model.WaitlistEntry
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("plot_id = ? AND user_id = ? AND status IN ?", plotID, userID, waitlistActiveStatuses()).
		First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *waitlistRepository) HeadForUpdate(tx *gorm.DB, plotID uint) (*model.WaitlistEntry, error) {
	var e model.WaitlistEntry
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("plot_id = ? AND status IN ?", plotID, waitlistActiveStatuses()).
		Preload("User").Preload("Plot").
		Order("CASE status WHEN 'invited' THEN 0 ELSE 1 END, registered_at ASC, id ASC").
		First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *waitlistRepository) ListActiveByPlotForUpdate(tx *gorm.DB, plotID uint) ([]model.WaitlistEntry, error) {
	var list []model.WaitlistEntry
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("plot_id = ? AND status IN ?", plotID, waitlistActiveStatuses()).
		Order("registered_at ASC, id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *waitlistRepository) ListByPlot(plotID uint, status string, pq util.PageQuery) ([]model.WaitlistEntry, int64, error) {
	var list []model.WaitlistEntry
	var total int64
	q := r.db.Model(&model.WaitlistEntry{}).Preload("User").Preload("Plot").Where("plot_id = ?", plotID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := util.Paginate(q.Order("registered_at DESC, id DESC"), pq).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *waitlistRepository) ListByUser(userID uint, status string, pq util.PageQuery) ([]model.WaitlistEntry, int64, error) {
	var list []model.WaitlistEntry
	var total int64
	q := r.db.Model(&model.WaitlistEntry{}).Preload("Plot").Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	} else {
		q = q.Where("status IN ?", waitlistActiveStatuses())
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := util.Paginate(q.Order("registered_at DESC, id DESC"), pq).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *waitlistRepository) CountAhead(tx *gorm.DB, entry *model.WaitlistEntry) (int64, error) {
	db := tx
	if db == nil {
		db = r.db
	}
	var n int64
	err := db.Model(&model.WaitlistEntry{}).
		Where("plot_id = ? AND status IN ?", entry.PlotID, waitlistActiveStatuses()).
		Where("registered_at < ? OR (registered_at = ? AND id < ?)", entry.RegisteredAt, entry.RegisteredAt, entry.ID).
		Count(&n).Error
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (r *waitlistRepository) CountActiveByPlot(plotID uint) (int64, error) {
	var n int64
	err := r.db.Model(&model.WaitlistEntry{}).
		Where("plot_id = ? AND status IN ?", plotID, waitlistActiveStatuses()).
		Count(&n).Error
	return n, err
}

func (r *waitlistRepository) ListAll(plotID uint, status string, pq util.PageQuery) ([]model.WaitlistEntry, int64, error) {
	var list []model.WaitlistEntry
	var total int64
	q := r.db.Model(&model.WaitlistEntry{}).Preload("User").Preload("Plot")
	if plotID > 0 {
		q = q.Where("plot_id = ?", plotID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := util.Paginate(q.Order("registered_at DESC, id DESC"), pq).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *waitlistRepository) ListExpiredInvited(now time.Time, limit int) ([]model.WaitlistEntry, error) {
	var list []model.WaitlistEntry
	err := r.db.Preload("Plot").
		Where("status = ? AND confirm_expires_at IS NOT NULL AND confirm_expires_at < ?", string(constants.WaitlistInvited), now).
		Order("confirm_expires_at ASC, id ASC").
		Limit(limit).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *waitlistRepository) ListInvitedPlotIDs() ([]uint, error) {
	var ids []uint
	err := r.db.Model(&model.WaitlistEntry{}).
		Where("status = ?", string(constants.WaitlistInvited)).
		Distinct().
		Pluck("plot_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}
