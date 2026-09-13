package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/dto"
	"github.com/communitygarden/server/internal/middleware"
	"github.com/communitygarden/server/internal/service"
	"github.com/communitygarden/server/internal/util"
)

// WaitlistHandler 地块候补与递补接口。
type WaitlistHandler struct {
	waitlistService *service.WaitlistService
	audit           middleware.AuditWriter
}

// NewWaitlistHandler 构造候补接口。
func NewWaitlistHandler(waitlistService *service.WaitlistService, audit middleware.AuditWriter) *WaitlistHandler {
	return &WaitlistHandler{waitlistService: waitlistService, audit: audit}
}

// Join 登记候补 POST /plots/:id/waitlist。
func (h *WaitlistHandler) Join(c *gin.Context) {
	plotID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "路径参数 id 必须为正整数")
		return
	}
	var req dto.JoinWaitlistRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			util.Fail(c, http.StatusBadRequest, constants.CodeValidationFailed, constants.ErrorText[constants.CodeValidationFailed]+": "+err.Error())
			return
		}
	}
	claims, _ := util.GetClaims(c)
	view, err := h.waitlistService.Join(uint(plotID), claims.UserID, claims.Role, claims.Username, req.Remark)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "JOIN_WAITLIST", "waitlist", strconv.FormatUint(uint64(view.Entry.ID), 10),
		"用户登记地块候补 plot_id="+strconv.FormatUint(plotID, 10), c.ClientIP(), util.GetRequestID(c))
	util.OK(c, service.ToViewDTO(view))
}

// ListMine 我的候补（默认仅有效记录；status=all 返回全部）。
func (h *WaitlistHandler) ListMine(c *gin.Context) {
	claims, _ := util.GetClaims(c)
	pq := util.ParsePageQuery(c)
	status := c.Query("status")
	views, total, err := h.waitlistService.ListMine(claims.UserID, status, pq)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	list := make([]*dto.WaitlistOutDTO, 0, len(views))
	for i := range views {
		list = append(list, service.ToViewDTO(&views[i]))
	}
	util.OK(c, util.PageResult{List: list, Total: total, Page: pq.Page, PageSize: pq.PageSize})
}

// Confirm 队首确认认养。
func (h *WaitlistHandler) Confirm(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "路径参数 id 必须为正整数")
		return
	}
	claims, _ := util.GetClaims(c)
	entry, plot, err := h.waitlistService.Confirm(uint(id), claims.UserID, claims.Role, claims.Username)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "CONFIRM_WAITLIST", "waitlist", strconv.FormatUint(id, 10),
		"队首确认认养地块 "+plot.Code, c.ClientIP(), util.GetRequestID(c))
	util.OK(c, gin.H{"waitlist": dto.ToWaitlistOutDTO(entry, 1), "plot": dto.ToPlotOutDTO(plot)})
}

// Cancel 用户主动放弃候补。
func (h *WaitlistHandler) Cancel(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "路径参数 id 必须为正整数")
		return
	}
	claims, _ := util.GetClaims(c)
	entry, err := h.waitlistService.Cancel(uint(id), claims.UserID, claims.Role, claims.Username)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "CANCEL_WAITLIST", "waitlist", strconv.FormatUint(id, 10),
		"用户主动放弃候补 plot_id="+strconv.FormatUint(uint64(entry.PlotID), 10), c.ClientIP(), util.GetRequestID(c))
	util.OK(c, dto.ToWaitlistOutDTO(entry, 0))
}

// ListAll 管理端查看候补队列（可按 plot_id/status 过滤）。
func (h *WaitlistHandler) ListAll(c *gin.Context) {
	pq := util.ParsePageQuery(c)
	status := c.Query("status")
	var plotID uint
	if v := c.Query("plot_id"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == 0 {
			util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "查询参数 plot_id 必须为正整数")
			return
		}
		plotID = uint(n)
	}
	views, total, err := h.waitlistService.ListAll(plotID, status, pq)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	list := make([]*dto.WaitlistOutDTO, 0, len(views))
	for i := range views {
		list = append(list, service.ToViewDTO(&views[i]))
	}
	util.OK(c, util.PageResult{List: list, Total: total, Page: pq.Page, PageSize: pq.PageSize})
}

// AdminRemove 管理员移除候选。
func (h *WaitlistHandler) AdminRemove(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "路径参数 id 必须为正整数")
		return
	}
	var req dto.AdminRemoveWaitlistRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			util.Fail(c, http.StatusBadRequest, constants.CodeValidationFailed, constants.ErrorText[constants.CodeValidationFailed]+": "+err.Error())
			return
		}
	}
	claims, _ := util.GetClaims(c)
	entry, err := h.waitlistService.AdminRemove(uint(id), claims.UserID, claims.Role, req.Remark)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "REMOVE_WAITLIST", "waitlist", strconv.FormatUint(id, 10),
		"管理员移除候选 plot_id="+strconv.FormatUint(uint64(entry.PlotID), 10), c.ClientIP(), util.GetRequestID(c))
	util.OK(c, dto.ToWaitlistOutDTO(entry, 0))
}

// AdminExpire 管理员处理异常：将逾期候选置为过期并顺延。
func (h *WaitlistHandler) AdminExpire(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "路径参数 id 必须为正整数")
		return
	}
	claims, _ := util.GetClaims(c)
	entry, advanced, err := h.waitlistService.AdminExpire(uint(id), claims.UserID, claims.Role)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	advancedID := uint(0)
	if advanced != nil {
		advancedID = advanced.ID
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "EXPIRE_WAITLIST", "waitlist", strconv.FormatUint(id, 10),
		"管理员处理逾期异常，顺延至 waitlist_id="+strconv.FormatUint(uint64(advancedID), 10), c.ClientIP(), util.GetRequestID(c))
	util.OK(c, gin.H{"waitlist": dto.ToWaitlistOutDTO(entry, 0), "advanced_to": advancedID})
}
