package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/dto"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// 默认队首确认时长（分钟），配置缺省/非法时兜底。
const defaultConfirmTTL = 30 * time.Minute

// WaitlistView 候补记录 + 实时排队位置（position 从 1 开始，终态记录为 0）。
type WaitlistView struct {
	Entry    model.WaitlistEntry
	Position int
}

// WaitlistPromoter 释放流程对候补递补的依赖（由 WaitlistService 实现）。
// PlotService 通过该接口在释放事务内触发队首邀请，避免构造循环依赖。
type WaitlistPromoter interface {
	// PromoteAfterRelease 地块释放事务内：若存在有效候补则邀请队首并把地块置为
	// pending（候补确认中），返回受邀记录；队列为空返回 nil（地块保持 available）。
	PromoteAfterRelease(tx *gorm.DB, plot *model.Plot) (*model.WaitlistEntry, error)
}

// WaitlistService 地块候补与递补服务。
//
// 并发约定（避免 AB-BA 死锁）：所有写事务统一按「先地块行 FOR UPDATE、
// 后候补行 FOR UPDATE」的顺序加锁；释放流程 PlotService.Release 亦遵循同序。
type WaitlistService struct {
	waitRepo   repository.WaitlistRepository
	plotRepo   repository.PlotRepository
	db         *gorm.DB
	logger     *slog.Logger
	confirmTTL time.Duration
}

// NewWaitlistService 构造候补服务；confirmMinutes 为队首确认认养的限定时间。
func NewWaitlistService(waitRepo repository.WaitlistRepository, plotRepo repository.PlotRepository, db *gorm.DB, logger *slog.Logger, confirmMinutes int) *WaitlistService {
	ttl := time.Duration(confirmMinutes) * time.Minute
	if ttl <= 0 {
		ttl = defaultConfirmTTL
	}
	return &WaitlistService{waitRepo: waitRepo, plotRepo: plotRepo, db: db, logger: logger, confirmTTL: ttl}
}

// ConfirmTTL 暴露确认时长（后台扫描/展示复用）。
func (s *WaitlistService) ConfirmTTL() time.Duration { return s.confirmTTL }

// StartSweeper 启动后台逾期扫描协程，stop 关闭后退出。
func (s *WaitlistService) StartSweeper(interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := s.SweepOverdue(); err != nil {
				s.logger.Warn(constants.LogInternalError, "err", fmt.Errorf("waitlist sweep: %w", err))
			}
		}
	}
}

// Join 登记候补（事务 + 行锁）。仅已认养（adopted）或待释放（harvested）地块可登记；
// 同一人同一地块仅允许一条有效记录，按登记时间排队。
func (s *WaitlistService) Join(plotID, userID uint, role, username, remark string) (*WaitlistView, error) {
	var view *WaitlistView
	err := s.db.Transaction(func(tx *gorm.DB) error {
		plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		switch constants.PlotStatus(plot.Status) {
		case constants.PlotStatusAdopted, constants.PlotStatusHarvested:
			// 允许候补
		case constants.PlotStatusPending:
			return util.NewAppError(constants.CodeWaitlistNotAllowed, 409, fmt.Sprintf("地块 %s 处于候补确认期，请稍后再登记候补", plot.Code))
		default:
			return util.NewAppError(constants.CodeWaitlistNotAllowed, 409, fmt.Sprintf("地块 %s 当前状态为 %s，空闲地块可直接认养，无需候补", plot.Code, util.PlotStatusText(plot.Status)))
		}
		if plot.AdopterID != nil && *plot.AdopterID == userID {
			return util.NewAppError(constants.CodeWaitlistNotAllowed, 400, fmt.Sprintf("用户 id=%d 已是地块 %s 的认养人，不能候补自己的地块", userID, plot.Code))
		}
		if existing, err := s.waitRepo.FindActiveForUpdate(tx, plotID, userID); err == nil && existing != nil {
			return util.NewAppError(constants.CodeWaitlistDuplicate, 409, fmt.Sprintf("用户 %s 已在地块 %s 的候补队列中（候补 id=%d，状态 %s），请勿重复登记", username, plot.Code, existing.ID, util.WaitlistStatusText(existing.Status)))
		} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}

		now := time.Now()
		entry := &model.WaitlistEntry{
			PlotID:       plotID,
			UserID:       userID,
			Status:       string(constants.WaitlistWaiting),
			RegisteredAt: now,
			Remark:       remark,
		}
		entry.RefreshActiveKey()
		if err := s.waitRepo.Create(tx, entry); err != nil {
			if errors.Is(err, repository.ErrWaitlistDuplicate) {
				return util.NewAppError(constants.CodeWaitlistDuplicate, 409, fmt.Sprintf("用户 %s 已在地块 %s 的候补队列中，请勿重复登记", username, plot.Code))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		ahead, err := s.waitRepo.CountAhead(tx, entry)
		if err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		view = &WaitlistView{Entry: *entry, Position: int(ahead) + 1}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogWaitlistRegistered, "waitlist_id", view.Entry.ID, "plot_id", plotID, "user_id", userID, "position", view.Position)
	return view, nil
}

// loadLockedForConfirm 按「地块 → 候补行」顺序加锁并重新读取候补记录。
func (s *WaitlistService) loadLockedForConfirm(tx *gorm.DB, entryID uint) (*model.Plot, *model.WaitlistEntry, error) {
	pre, err := s.waitRepo.FindByID(entryID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, util.NewAppError(constants.CodeWaitlistNotFound, 404, fmt.Sprintf("候补记录实体 id=%d 不存在", entryID))
		}
		return nil, nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	plot, err := s.plotRepo.FindByIDForUpdate(tx, pre.PlotID)
	if err != nil {
		return nil, nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	entry, err := s.waitRepo.FindByIDForUpdate(tx, entryID)
	if err != nil {
		return nil, nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return plot, entry, nil
}

// Confirm 队首在限定时间内确认认养（invited -> confirmed，地块 pending -> adopted）。
// 若确认已逾期，本次调用同时完成逾期标记并自动顺延到下一位。
func (s *WaitlistService) Confirm(entryID, userID uint, role, username string) (*model.WaitlistEntry, *model.Plot, error) {
	var entry *model.WaitlistEntry
	var plot *model.Plot
	// confirmErr 表示“事务需提交、但接口要返回失败”的业务结果（如本次调用触发了逾期顺延）。
	var confirmErr *util.AppError
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		plot, entry, err = s.loadLockedForConfirm(tx, entryID)
		if err != nil {
			return err
		}
		if entry.UserID != userID {
			return util.NewAppError(constants.CodeForbidden, 403, fmt.Sprintf("角色 %s 用户 %s 无权确认候补 id=%d（仅候选本人可确认）", util.RoleText(role), username, entryID))
		}
		now := time.Now()
		switch constants.WaitlistStatus(entry.Status) {
		case constants.WaitlistWaiting:
			return util.NewAppError(constants.CodeWaitlistNotInvited, 409, fmt.Sprintf("候补 id=%d 尚未进入确认期，请等待队首递补", entryID))
		case constants.WaitlistInvited:
			if entry.ConfirmExpiresAt != nil && now.After(*entry.ConfirmExpiresAt) {
				// 逾期：先在同一事务内完成顺延（必须提交），再以业务错误告知调用方。
				deadline := entry.ConfirmExpiresAt.Format("2006-01-02 15:04:05")
				if _, advErr := s.expireLocked(tx, plot, entry, now); advErr != nil {
					return advErr
				}
				confirmErr = util.NewAppError(constants.CodeWaitlistConfirmExpired, 409, fmt.Sprintf("候补 id=%d 确认截止 %s 已逾期，资格已自动顺延给下一位", entryID, deadline))
				return nil
			}
		case constants.WaitlistExpired:
			return util.NewAppError(constants.CodeWaitlistConfirmExpired, 409, fmt.Sprintf("候补 id=%d 已逾时顺延，不能确认", entryID))
		default:
			return util.NewAppError(constants.CodeWaitlistAlreadyProcessed, 409, fmt.Sprintf("候补 id=%d 当前状态为 %s，已处理完毕", entryID, util.WaitlistStatusText(entry.Status)))
		}

		if plot.Status != string(constants.PlotStatusPending) {
			// 异常状态：地块已不在确认期（如被管理员另行处理），候选不可再确认。
			entry.MarkTerminal(string(constants.WaitlistExpired), now)
			if uErr := s.waitRepo.Update(tx, entry); uErr != nil {
				return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(uErr)
			}
			confirmErr = util.NewAppError(constants.CodePlotNotAvailable, 409, fmt.Sprintf("地块 %s 当前状态为 %s，已离开候补确认期，请联系管理员处理", plot.Code, util.PlotStatusText(plot.Status)))
			return nil
		}
		plot.Status = string(constants.PlotStatusAdopted)
		plot.AdopterID = &entry.UserID
		if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		entry.MarkTerminal(string(constants.WaitlistConfirmed), now)
		if err := s.waitRepo.Update(tx, entry); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if confirmErr != nil {
		return nil, nil, confirmErr
	}
	s.logger.Info(constants.LogWaitlistConfirmed, "waitlist_id", entry.ID, "plot_id", entry.PlotID, "user_id", userID)
	return entry, plot, nil
}

// Cancel 用户主动放弃候补（waiting 直接终止；invited 终止后自动顺延下一位）。
func (s *WaitlistService) Cancel(entryID, userID uint, role, username string) (*model.WaitlistEntry, error) {
	var entry *model.WaitlistEntry
	var advanced *model.WaitlistEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		pre, err := s.waitRepo.FindByID(entryID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeWaitlistNotFound, 404, fmt.Sprintf("候补记录实体 id=%d 不存在", entryID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		// invited 放弃会顺延，涉及地块，按「地块 → 候补行」顺序加锁；
		// waiting 放弃只需候补行锁（不申请地块锁，不构成死锁环）。
		var plot *model.Plot
		if pre.Status == string(constants.WaitlistInvited) {
			plot, err = s.plotRepo.FindByIDForUpdate(tx, pre.PlotID)
			if err != nil {
				return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
			}
		}
		entry, err = s.waitRepo.FindByIDForUpdate(tx, entryID)
		if err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if entry.UserID != userID {
			return util.NewAppError(constants.CodeForbidden, 403, fmt.Sprintf("角色 %s 用户 %s 无权放弃候补 id=%d", util.RoleText(role), username, entryID))
		}
		if !entry.IsActive() {
			return util.NewAppError(constants.CodeWaitlistAlreadyProcessed, 409, fmt.Sprintf("候补 id=%d 当前状态为 %s，无需放弃", entryID, util.WaitlistStatusText(entry.Status)))
		}
		wasInvited := entry.Status == string(constants.WaitlistInvited)
		now := time.Now()
		entry.MarkTerminal(string(constants.WaitlistCancelled), now)
		if err := s.waitRepo.Update(tx, entry); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if wasInvited && plot != nil && plot.Status == string(constants.PlotStatusPending) {
			advanced, err = s.advanceLocked(tx, plot, now)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogWaitlistCancelled, "waitlist_id", entry.ID, "plot_id", entry.PlotID, "user_id", userID)
	if advanced != nil {
		s.logger.Info(constants.LogWaitlistInvited, "waitlist_id", advanced.ID, "plot_id", advanced.PlotID, "user_id", advanced.UserID, "deadline", advanced.ConfirmExpiresAt.Format(time.RFC3339))
	}
	return entry, nil
}

// AdminRemove 管理员移除候选；若移除的是确认期队首则自动顺延下一位。
func (s *WaitlistService) AdminRemove(entryID, operatorID uint, operatorRole, remark string) (*model.WaitlistEntry, error) {
	var entry *model.WaitlistEntry
	var advanced *model.WaitlistEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		pre, err := s.waitRepo.FindByID(entryID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeWaitlistNotFound, 404, fmt.Sprintf("候补记录实体 id=%d 不存在", entryID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		var plot *model.Plot
		if pre.Status == string(constants.WaitlistInvited) {
			plot, err = s.plotRepo.FindByIDForUpdate(tx, pre.PlotID)
			if err != nil {
				return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
			}
		}
		entry, err = s.waitRepo.FindByIDForUpdate(tx, entryID)
		if err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if !entry.IsActive() {
			return util.NewAppError(constants.CodeWaitlistAlreadyProcessed, 409, fmt.Sprintf("候补 id=%d 当前状态为 %s，不能重复移除", entryID, util.WaitlistStatusText(entry.Status)))
		}
		if remark != "" {
			if entry.Remark != "" {
				entry.Remark = entry.Remark + "；管理员备注：" + remark
			} else {
				entry.Remark = "管理员备注：" + remark
			}
		}
		wasInvited := entry.Status == string(constants.WaitlistInvited)
		now := time.Now()
		entry.MarkTerminal(string(constants.WaitlistRemoved), now)
		if err := s.waitRepo.Update(tx, entry); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if wasInvited && plot != nil && plot.Status == string(constants.PlotStatusPending) {
			advanced, err = s.advanceLocked(tx, plot, now)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogWaitlistRemoved, "waitlist_id", entry.ID, "plot_id", entry.PlotID, "user_id", entry.UserID, "operator", operatorRole)
	if advanced != nil {
		s.logger.Info(constants.LogWaitlistInvited, "waitlist_id", advanced.ID, "plot_id", advanced.PlotID, "user_id", advanced.UserID, "deadline", advanced.ConfirmExpiresAt.Format(time.RFC3339))
	}
	return entry, nil
}

// AdminExpire 管理员处理异常：手动将逾期未确认的候选置为 expired 并顺延下一位。
func (s *WaitlistService) AdminExpire(entryID, operatorID uint, operatorRole string) (*model.WaitlistEntry, *model.WaitlistEntry, error) {
	var entry, advanced *model.WaitlistEntry
	err := s.db.Transaction(func(tx *gorm.DB) error {
		plot, locked, lErr := s.loadLockedForConfirm(tx, entryID)
		if lErr != nil {
			return lErr
		}
		entry = locked
		if entry.Status != string(constants.WaitlistInvited) {
			return util.NewAppError(constants.CodeWaitlistAlreadyProcessed, 409, fmt.Sprintf("候补 id=%d 当前状态为 %s，不在确认期", entryID, util.WaitlistStatusText(entry.Status)))
		}
		now := time.Now()
		var aErr error
		advanced, aErr = s.expireLocked(tx, plot, entry, now)
		return aErr
	})
	if err != nil {
		return nil, nil, err
	}
	s.logger.Info(constants.LogWaitlistExpired, "waitlist_id", entry.ID, "plot_id", entry.PlotID, "user_id", entry.UserID, "advanced_to", advancedID(advanced))
	return entry, advanced, nil
}

// GetByID 查询候补详情。
func (s *WaitlistService) GetByID(id uint) (*model.WaitlistEntry, error) {
	e, err := s.waitRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeWaitlistNotFound, 404, fmt.Sprintf("候补记录实体 id=%d 不存在", id))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return e, nil
}

// ListMine 我的候补（默认仅有效记录，status=all 返回全部），附带实时排队位置。
func (s *WaitlistService) ListMine(userID uint, status string, pq util.PageQuery) ([]WaitlistView, int64, error) {
	entries, total, err := s.waitRepo.ListByUser(userID, status, pq)
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return s.withPositions(entries), total, nil
}

// ListByPlot 查看指定地块的候补队列（管理端）。
func (s *WaitlistService) ListByPlot(plotID uint, status string, pq util.PageQuery) ([]WaitlistView, int64, error) {
	if plotID == 0 {
		return nil, 0, util.NewAppError(constants.CodeBadRequest, 400, "查询参数 plot_id 必须为正整数")
	}
	entries, total, err := s.waitRepo.ListByPlot(plotID, status, pq)
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return s.withPositions(entries), total, nil
}

// ListAll 管理端全量候补队列（可按地块/状态过滤）。
func (s *WaitlistService) ListAll(plotID uint, status string, pq util.PageQuery) ([]WaitlistView, int64, error) {
	entries, total, err := s.waitRepo.ListAll(plotID, status, pq)
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return s.withPositions(entries), total, nil
}

// withPositions 为有效记录计算排队位置（按登记时间，invited 队首为 1）。
func (s *WaitlistService) withPositions(entries []model.WaitlistEntry) []WaitlistView {
	views := make([]WaitlistView, 0, len(entries))
	for i := range entries {
		v := WaitlistView{Entry: entries[i]}
		if entries[i].IsActive() {
			ahead, err := s.waitRepo.CountAhead(nil, &entries[i])
			if err == nil {
				v.Position = int(ahead) + 1
			}
		}
		views = append(views, v)
	}
	return views
}

// PromoteAfterRelease 释放事务内递补队首（WaitlistPromoter 实现）。
// 队列为空时地块回到 available；有候选时邀请队首并把地块置为 pending。
func (s *WaitlistService) PromoteAfterRelease(tx *gorm.DB, plot *model.Plot) (*model.WaitlistEntry, error) {
	plot.Status = string(constants.PlotStatusAvailable)
	plot.AdopterID = nil
	head, err := s.waitRepo.HeadForUpdate(tx, plot.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			if uErr := s.plotRepo.UpdateWithTx(tx, plot); uErr != nil {
				return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(uErr)
			}
			return nil, nil
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	now := time.Now()
	head.MarkInvited(now, now.Add(s.confirmTTL))
	if err := s.waitRepo.Update(tx, head); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	plot.Status = string(constants.PlotStatusPending)
	if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return head, nil
}

// advanceLocked 队首资格终止后顺延：邀请下一位；无候选则地块回到空闲池。
// 调用方须已按「地块 → 候补行」顺序持有相关行锁。
func (s *WaitlistService) advanceLocked(tx *gorm.DB, plot *model.Plot, now time.Time) (*model.WaitlistEntry, error) {
	head, err := s.waitRepo.HeadForUpdate(tx, plot.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			plot.Status = string(constants.PlotStatusAvailable)
			plot.AdopterID = nil
			if uErr := s.plotRepo.UpdateWithTx(tx, plot); uErr != nil {
				return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(uErr)
			}
			return nil, nil
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	head.MarkInvited(now, now.Add(s.confirmTTL))
	if err := s.waitRepo.Update(tx, head); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	plot.Status = string(constants.PlotStatusPending)
	if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return head, nil
}

// expireLocked 将逾时队首置 expired 并顺延（调用方已持有地块行与该行锁）。
func (s *WaitlistService) expireLocked(tx *gorm.DB, plot *model.Plot, entry *model.WaitlistEntry, now time.Time) (*model.WaitlistEntry, error) {
	entry.MarkTerminal(string(constants.WaitlistExpired), now)
	if err := s.waitRepo.Update(tx, entry); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	if plot.Status != string(constants.PlotStatusPending) {
		return nil, nil
	}
	return s.advanceLocked(tx, plot, now)
}

// SweepOverdue 后台定时任务：逾期未确认的邀请自动顺延；并自愈“地块 pending 但无
// invited 候选”的异常状态。返回本轮顺延的候选数。
func (s *WaitlistService) SweepOverdue() (int, error) {
	now := time.Now()
	expired, err := s.waitRepo.ListExpiredInvited(now, 200)
	if err != nil {
		return 0, err
	}
	advanced := 0
	for i := range expired {
		var next *model.WaitlistEntry
		txErr := s.db.Transaction(func(tx *gorm.DB) error {
			// 统一加锁顺序：先地块，后候补行。
			plot, pErr := s.plotRepo.FindByIDForUpdate(tx, expired[i].PlotID)
			if pErr != nil {
				if errors.Is(pErr, repository.ErrNotFound) {
					return nil
				}
				return pErr
			}
			locked, lErr := s.waitRepo.FindByIDForUpdate(tx, expired[i].ID)
			if lErr != nil {
				if errors.Is(lErr, repository.ErrNotFound) {
					return nil
				}
				return lErr
			}
			if locked.Status != string(constants.WaitlistInvited) || locked.ConfirmExpiresAt == nil || !now.After(*locked.ConfirmExpiresAt) {
				return nil
			}
			var aErr error
			next, aErr = s.expireLocked(tx, plot, locked, now)
			return aErr
		})
		if txErr != nil {
			s.logger.Warn(constants.LogInternalError, "err", fmt.Errorf("sweep waitlist id=%d: %w", expired[i].ID, txErr))
			continue
		}
		advanced++
		s.logger.Info(constants.LogWaitlistExpired, "waitlist_id", expired[i].ID, "plot_id", expired[i].PlotID, "user_id", expired[i].UserID, "advanced_to", advancedID(next))
	}

	// 异常自愈：pending 地块若已无 invited 候选，则顺延下一位或回到空闲池。
	pendingIDs, err := s.plotRepo.FindIDsByStatus(string(constants.PlotStatusPending))
	if err != nil {
		return advanced, err
	}
	invitedPlots, err := s.waitRepo.ListInvitedPlotIDs()
	if err != nil {
		return advanced, err
	}
	invitedSet := make(map[uint]struct{}, len(invitedPlots))
	for _, id := range invitedPlots {
		invitedSet[id] = struct{}{}
	}
	for _, plotID := range pendingIDs {
		if _, ok := invitedSet[plotID]; ok {
			continue
		}
		txErr := s.db.Transaction(func(tx *gorm.DB) error {
			plot, pErr := s.plotRepo.FindByIDForUpdate(tx, plotID)
			if pErr != nil {
				return pErr
			}
			if plot.Status != string(constants.PlotStatusPending) {
				return nil
			}
			_, aErr := s.advanceLocked(tx, plot, now)
			return aErr
		})
		if txErr != nil {
			s.logger.Warn(constants.LogInternalError, "err", fmt.Errorf("repair pending plot id=%d: %w", plotID, txErr))
		}
	}
	s.logger.Info(constants.LogWaitlistSweep, "expired", advanced, "invited", len(invitedPlots))
	return advanced, nil
}

// ToViewDTO 将 service 视图转为输出 DTO（handler 复用）。
func ToViewDTO(v *WaitlistView) *dto.WaitlistOutDTO {
	return dto.ToWaitlistOutDTO(&v.Entry, v.Position)
}

func advancedID(e *model.WaitlistEntry) uint {
	if e == nil {
		return 0
	}
	return e.ID
}
