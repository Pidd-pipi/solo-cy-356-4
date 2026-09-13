package model

import "time"

// WaitlistEntry 地块候补记录实体。
//
// 状态机（constants.WaitlistStatus）：
//
//	waiting（排队中）
//	  └─ 地块释放且成为队首 → invited（确认期内锁定地块）
//	        ├─ 队首在确认截止前确认 → confirmed（地块认养到其名下）
//	        ├─ 队首逾期未确认       → expired（自动顺延到下一位）
//	        └─ 队首主动放弃/管理员移除 → cancelled / removed（自动顺延到下一位）
//
// 同一用户对同一地块仅允许存在一条有效记录（waiting/invited）：
// 由 (plot_id, user_id, active_key) 复合唯一索引保证——active_key 仅对有效记录
// 取非空值 "active"，终态记录为 NULL（PostgreSQL/SQLite 唯一索引中 NULL 互不相同，
// 因此终态后可重新候补）；服务层事务再做一次业务校验并兜底竞态错误。
type WaitlistEntry struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	PlotID           uint       `gorm:"not null;uniqueIndex:uniq_waitlist_active,priority:1;index:idx_waitlist_queue,priority:1" json:"plot_id"`
	UserID           uint       `gorm:"not null;uniqueIndex:uniq_waitlist_active,priority:2;index:idx_waitlist_user" json:"user_id"`
	ActiveKey        *string    `gorm:"size:16;uniqueIndex:uniq_waitlist_active,priority:3" json:"-"`
	User             *User      `gorm:"foreignKey:UserID" json:"user"`
	Plot             *Plot      `gorm:"foreignKey:PlotID" json:"plot"`
	Status           string     `gorm:"size:32;not null;default:waiting;index:idx_waitlist_queue,priority:2;index" json:"status"`
	InvitedAt        *time.Time `json:"invited_at"`
	ConfirmExpiresAt *time.Time `gorm:"index" json:"confirm_expires_at"`
	ConfirmedAt      *time.Time `json:"confirmed_at"`
	RegisteredAt     time.Time  `gorm:"not null;index:idx_waitlist_queue,priority:3;index" json:"registered_at"`
	Remark           string     `gorm:"size:256" json:"remark"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// WaitlistActiveKey 有效候补记录的唯一索引哨兵值。
const WaitlistActiveKey = "active"

// IsActive 是否为有效候补记录（排队中或已邀请确认）。
func (w *WaitlistEntry) IsActive() bool {
	return w.Status == "waiting" || w.Status == "invited"
}

// RefreshActiveKey 按当前状态刷新部分唯一索引哨兵列。
func (w *WaitlistEntry) RefreshActiveKey() {
	if w.IsActive() {
		key := WaitlistActiveKey
		w.ActiveKey = &key
	} else {
		w.ActiveKey = nil
	}
}

// MarkInvited 进入队首确认期。
func (w *WaitlistEntry) MarkInvited(at, deadline time.Time) {
	w.Status = "invited"
	w.InvitedAt = &at
	w.ConfirmExpiresAt = &deadline
	w.RefreshActiveKey()
}

// MarkTerminal 流转到终态并清空确认期与唯一索引哨兵值。
func (w *WaitlistEntry) MarkTerminal(status string, at time.Time) {
	w.Status = status
	w.ConfirmExpiresAt = nil
	if status == "confirmed" {
		w.ConfirmedAt = &at
	}
	w.RefreshActiveKey()
}
