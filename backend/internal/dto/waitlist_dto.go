package dto

import (
	"time"

	"github.com/communitygarden/server/internal/model"
)

// JoinWaitlistRequest 登记候补请求。
type JoinWaitlistRequest struct {
	Remark string `json:"remark" binding:"omitempty,max=256"`
}

// AdminRemoveWaitlistRequest 管理员移除候选。
type AdminRemoveWaitlistRequest struct {
	Remark string `json:"remark" binding:"omitempty,max=256"`
}

// WaitlistPlotDTO 候补记录内嵌的地块摘要。
type WaitlistPlotDTO struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Status string `json:"status"`
}

// WaitlistOutDTO 候补记录输出（我的候补页与管理端队列共用）。
type WaitlistOutDTO struct {
	ID               uint             `json:"id"`
	PlotID           uint             `json:"plot_id"`
	UserID           uint             `json:"user_id"`
	Status           string           `json:"status"`
	Position         int              `json:"position"` // 排队位置，1 为队首；非有效记录为 0
	Remark           string           `json:"remark"`
	User             *UserOutDTO      `json:"user"`
	Plot             *WaitlistPlotDTO `json:"plot"`
	RegisteredAt     string           `json:"registered_at"`
	InvitedAt        string           `json:"invited_at"`
	ConfirmExpiresAt string           `json:"confirm_expires_at"`
	ConfirmedAt      string           `json:"confirmed_at"`
	RemainSeconds    int64            `json:"remain_seconds"` // 确认剩余秒数（invited 状态）
	CreatedAt        string           `json:"created_at"`
}

// ToWaitlistOutDTO 模型转 DTO；position 为按登记时间计算的排队位置（<0 表示未知）。
func ToWaitlistOutDTO(e *model.WaitlistEntry, position int) *WaitlistOutDTO {
	out := &WaitlistOutDTO{
		ID:           e.ID,
		PlotID:       e.PlotID,
		UserID:       e.UserID,
		Status:       e.Status,
		Position:     position,
		Remark:       e.Remark,
		RegisteredAt: formatTime(e.RegisteredAt),
		CreatedAt:    formatTime(e.CreatedAt),
	}
	if e.User != nil {
		out.User = ToUserOutDTO(e.User)
	}
	if e.Plot != nil {
		out.Plot = &WaitlistPlotDTO{ID: e.Plot.ID, Name: e.Plot.Name, Code: e.Plot.Code, Status: e.Plot.Status}
	}
	if e.InvitedAt != nil {
		out.InvitedAt = formatTime(*e.InvitedAt)
	}
	if e.ConfirmExpiresAt != nil {
		out.ConfirmExpiresAt = formatTime(*e.ConfirmExpiresAt)
	}
	if e.ConfirmedAt != nil {
		out.ConfirmedAt = formatTime(*e.ConfirmedAt)
	}
	if e.Status == "invited" && e.ConfirmExpiresAt != nil {
		if d := time.Until(*e.ConfirmExpiresAt); d > 0 {
			out.RemainSeconds = int64(d.Seconds())
		}
	}
	return out
}

// formatTime 时间统一以带时区的绝对时刻（RFC3339，UTC，Z 结尾）下发，
// 避免“无时区墙上时间”被不同时区的浏览器按本地时区错解（如 UTC 服务 + 上海页面差 8 小时）。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
