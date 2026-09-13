package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/communitygarden/server/internal/config"
	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/handler"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/service"
	"github.com/communitygarden/server/internal/util"
)

var waitlistAPIDBCounter uint64

// waitlistAPITestEnv 面向外部接口的测试环境：内存 SQLite + 真实 service/handler +
// 真实 Auth/RBAC 中间件，仅注册 plots（含候补登记）与 waitlist 两组路由。
type waitlistAPITestEnv struct {
	t      *testing.T
	engine *gin.Engine
	db     *gorm.DB
	// svcs
	plotSvc *service.PlotService
	waitSvc *service.WaitlistService
	// users
	adminID, farmerID, u1ID, u2ID uint
	// plots
	plotA, plotB, plotC, plotD uint
	// tokens
	adminToken, farmerToken, u1Token, u2Token string
}

func newWaitlistAPITestEnv(t *testing.T) *waitlistAPITestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := atomic.AddUint64(&waitlistAPIDBCounter, 1)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:waitlist_api_%d?mode=memory&cache=shared", n)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Plot{}, &model.PlantingPlan{}, &model.HarvestRecord{},
		&model.DiaryEntry{}, &model.DiaryComment{}, &model.CommunityPost{}, &model.CommunityComment{},
		&model.WaitlistEntry{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{RunMode: "test", JWTSecret: "api-test-secret", JWTExpireHours: 72}
	logger := util.NewLogger("error")

	plotRepo := repository.NewPlotRepository(db)
	waitlistRepo := repository.NewWaitlistRepository(db)
	auditRepo := repository.NewAuditRepository(db)

	plotSvc := service.NewPlotService(plotRepo, db, logger)
	auditSvc := service.NewAuditService(auditRepo, logger)
	waitSvc := service.NewWaitlistService(waitlistRepo, plotRepo, db, logger, 30)
	plotSvc.SetWaitlistPromoter(waitSvc)

	plotHandler := handler.NewPlotHandler(plotSvc, auditSvc)
	waitlistHandler := handler.NewWaitlistHandler(waitSvc, auditSvc)

	r := New(
		cfg, logger, nil,
		(*handler.AuthHandler)(nil), (*handler.UserHandler)(nil),
		plotHandler, waitlistHandler,
		(*handler.PlantingPlanHandler)(nil), (*handler.HarvestHandler)(nil),
		(*handler.DiaryHandler)(nil), (*handler.CommunityHandler)(nil),
		(*handler.AuditHandler)(nil), (*handler.StatsHandler)(nil),
		auditSvc, nil,
	)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	r.registerPlots(v1)
	r.registerWaitlist(v1)

	env := &waitlistAPITestEnv{t: t, engine: engine, db: db, plotSvc: plotSvc, waitSvc: waitSvc}

	// 用户
	env.adminID = env.seedUser("api-admin", "admin")
	env.farmerID = env.seedUser("api-farmer", "farmer")
	env.u1ID = env.seedUser("api-u1", "citizen")
	env.u2ID = env.seedUser("api-u2", "citizen")
	env.adminToken = env.mustToken(env.adminID, "api-admin", "admin")
	env.farmerToken = env.mustToken(env.farmerID, "api-farmer", "farmer")
	env.u1Token = env.mustToken(env.u1ID, "api-u1", "citizen")
	env.u2Token = env.mustToken(env.u2ID, "api-u2", "citizen")

	// 地块：A/B/C 待释放（有认养人），D 已认养
	env.plotA = env.seedPlot("API-A", "harvested")
	env.plotB = env.seedPlot("API-B", "harvested")
	env.plotC = env.seedPlot("API-C", "harvested")
	env.plotD = env.seedPlot("API-D", "adopted")

	// A：u1、u2 排队后释放 → u1 受邀队首，u2 排队中
	env.mustJoin(env.plotA, env.u1ID, "api-u1")
	env.mustJoin(env.plotA, env.u2ID, "api-u2")
	if _, err := plotSvc.Release(env.plotA, env.farmerID, "farmer"); err != nil {
		t.Fatalf("release A: %v", err)
	}
	// B：u2 排队后主动放弃 → cancelled
	vb := env.mustJoin(env.plotB, env.u2ID, "api-u2")
	if _, err := waitSvc.Cancel(vb.Entry.ID, env.u2ID, "citizen", "api-u2"); err != nil {
		t.Fatalf("cancel B: %v", err)
	}
	// C：u2 成为队首受邀后逾期被扫描顺延 → expired（无其他候选，地块回空闲）
	env.mustJoin(env.plotC, env.u2ID, "api-u2")
	if _, err := plotSvc.Release(env.plotC, env.farmerID, "farmer"); err != nil {
		t.Fatalf("release C: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.Model(&model.WaitlistEntry{}).
		Where("plot_id = ? AND status = ?", env.plotC, "invited").
		Update("confirm_expires_at", past).Error; err != nil {
		t.Fatalf("backdate C: %v", err)
	}
	if _, err := waitSvc.SweepOverdue(); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	return env
}

func (e *waitlistAPITestEnv) seedUser(username, role string) uint {
	u := &model.User{Username: username, Password: "x", Nickname: username, Role: role, Status: "active"}
	if err := e.db.Create(u).Error; err != nil {
		e.t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func (e *waitlistAPITestEnv) seedPlot(code, status string) uint {
	owner := e.farmerID
	p := &model.Plot{Name: code, Code: code, Area: 10, SoilType: "loam", Sunlight: "full",
		Latitude: 31, Longitude: 121, Status: status, AdopterID: &owner}
	if err := e.db.Create(p).Error; err != nil {
		e.t.Fatalf("seed plot: %v", err)
	}
	return p.ID
}

func (e *waitlistAPITestEnv) mustJoin(plotID, userID uint, username string) *service.WaitlistView {
	v, err := e.waitSvc.Join(plotID, userID, "citizen", username, "")
	if err != nil {
		e.t.Fatalf("join plot=%d user=%d: %v", plotID, userID, err)
	}
	return v
}

func (e *waitlistAPITestEnv) mustToken(id uint, username, role string) string {
	tok, err := util.GenerateToken("api-test-secret", 72, id, username, role)
	if err != nil {
		e.t.Fatalf("generate token: %v", err)
	}
	return tok
}

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *waitlistAPITestEnv) do(method, path, token string, body any) (int, apiResp) {
	e.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)

	var resp apiResp
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			e.t.Fatalf("decode resp %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, resp
}

// 面向外部接口：管理端候补队列的鉴权——未登录 401、普通居民 403、管理员 200。
func TestWaitlistAPI_AdminQueueAuthz(t *testing.T) {
	env := newWaitlistAPITestEnv(t)

	// 未登录
	status, resp := env.do(http.MethodGet, "/api/v1/waitlist", "", nil)
	if status != http.StatusUnauthorized || resp.Code != constants.CodeUnauthorized {
		t.Fatalf("anonymous: status=%d code=%d, want 401/%d", status, resp.Code, constants.CodeUnauthorized)
	}

	// 普通居民被拒绝
	status, resp = env.do(http.MethodGet, "/api/v1/waitlist", env.u2Token, nil)
	if status != http.StatusForbidden || resp.Code != constants.CodeForbidden {
		t.Fatalf("citizen: status=%d code=%d, want 403/%d", status, resp.Code, constants.CodeForbidden)
	}

	// 居民也不能调用管理员移除接口
	status, _ = env.do(http.MethodPost, "/api/v1/waitlist/1/remove", env.u2Token, map[string]string{})
	if status != http.StatusForbidden {
		t.Fatalf("citizen remove: status=%d, want 403", status)
	}

	// 管理员可查看队列
	status, resp = env.do(http.MethodGet, "/api/v1/waitlist?page=1&page_size=50", env.adminToken, nil)
	if status != http.StatusOK || resp.Code != constants.CodeOK {
		t.Fatalf("admin list: status=%d code=%d msg=%s", status, resp.Code, resp.Message)
	}
	var page struct {
		Total int64 `json:"total"`
		List  []struct {
			ID uint `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Data, &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	// A(u1,u2) + B(u2) + C(u2) = 4 条
	if page.Total != 4 {
		t.Fatalf("admin queue total = %d, want 4", page.Total)
	}
}

// 面向外部接口：我的候补“全部记录”返回本人已取消/已逾时终态；默认只返回有效记录。
func TestWaitlistAPI_MineAllReturnsTerminalHistory(t *testing.T) {
	env := newWaitlistAPITestEnv(t)

	// 默认：u2 只有在 A 排队中的 1 条有效记录
	status, resp := env.do(http.MethodGet, "/api/v1/waitlist/mine", env.u2Token, nil)
	if status != http.StatusOK || resp.Code != constants.CodeOK {
		t.Fatalf("mine default: status=%d code=%d msg=%s", status, resp.Code, resp.Message)
	}
	var active struct {
		Total int64 `json:"total"`
		List  []struct {
			Status string `json:"status"`
		} `json:"list"`
	}
	json.Unmarshal(resp.Data, &active)
	if active.Total != 1 {
		t.Fatalf("mine active total = %d, want 1", active.Total)
	}
	for _, item := range active.List {
		if item.Status != "waiting" && item.Status != "invited" {
			t.Errorf("default mine leaked terminal status=%s", item.Status)
		}
	}

	// 全部记录：包含 waiting + cancelled + expired 共 3 条
	status, resp = env.do(http.MethodGet, "/api/v1/waitlist/mine?status=all&page_size=50", env.u2Token, nil)
	if status != http.StatusOK {
		t.Fatalf("mine all status=%d msg=%s", status, resp.Message)
	}
	var all struct {
		Total int64 `json:"total"`
		List  []struct {
			Status string `json:"status"`
			Plot   struct {
				Code string `json:"code"`
			} `json:"plot"`
			Position int `json:"position"`
		} `json:"list"`
	}
	json.Unmarshal(resp.Data, &all)
	if all.Total != 3 {
		t.Fatalf("mine all total = %d, want 3", all.Total)
	}
	seen := map[string]bool{}
	for _, item := range all.List {
		seen[item.Status] = true
	}
	for _, wantStatus := range []string{"waiting", "cancelled", "expired"} {
		if !seen[wantStatus] {
			t.Errorf("mine all missing terminal status %s, got=%v", wantStatus, seen)
		}
	}

	// 单状态过滤
	status, resp = env.do(http.MethodGet, "/api/v1/waitlist/mine?status=expired", env.u2Token, nil)
	if status != http.StatusOK {
		t.Fatalf("mine expired status=%d", status)
	}
	var expiredOnly struct {
		Total int64 `json:"total"`
	}
	json.Unmarshal(resp.Data, &expiredOnly)
	if expiredOnly.Total != 1 {
		t.Fatalf("mine expired total = %d, want 1", expiredOnly.Total)
	}
}

// 面向外部接口：非队首（仍在排队）确认被拒绝（409/2013）。
func TestWaitlistAPI_NonHeadConfirmRejected(t *testing.T) {
	env := newWaitlistAPITestEnv(t)

	// 找到 u2 在地块 A 上的 waiting 记录
	_, resp := env.do(http.MethodGet, "/api/v1/waitlist/mine", env.u2Token, nil)
	var mine struct {
		List []struct {
			ID     uint   `json:"id"`
			PlotID uint   `json:"plot_id"`
			Status string `json:"status"`
		} `json:"list"`
	}
	json.Unmarshal(resp.Data, &mine)
	var waitingID uint
	for _, item := range mine.List {
		if item.PlotID == env.plotA && item.Status == "waiting" {
			waitingID = item.ID
		}
	}
	if waitingID == 0 {
		t.Fatal("u2 waiting entry on plot A not found")
	}

	status, resp := env.do(http.MethodPost, "/api/v1/waitlist/"+itoa(waitingID)+"/confirm", env.u2Token, nil)
	if status != http.StatusConflict || resp.Code != constants.CodeWaitlistNotInvited {
		t.Fatalf("non-head confirm: status=%d code=%d msg=%s, want 409/%d", status, resp.Code, resp.Message, constants.CodeWaitlistNotInvited)
	}

	// u1 才是队首：u2 不能替他人确认
	var headID uint
	adminRespStatus, adminResp := env.do(http.MethodGet, "/api/v1/waitlist?plot_id="+itoa(env.plotA)+"&status=invited", env.adminToken, nil)
	if adminRespStatus != http.StatusOK {
		t.Fatalf("admin list invited: %d", adminRespStatus)
	}
	var invited struct {
		List []struct {
			ID     uint `json:"id"`
			UserID uint `json:"user_id"`
		} `json:"list"`
	}
	json.Unmarshal(adminResp.Data, &invited)
	for _, item := range invited.List {
		if item.UserID == env.u1ID {
			headID = item.ID
		}
	}
	if headID == 0 {
		t.Fatal("u1 invited head not found")
	}
	status, _ = env.do(http.MethodPost, "/api/v1/waitlist/"+itoa(headID)+"/confirm", env.u2Token, nil)
	if status != http.StatusForbidden {
		t.Fatalf("confirm others entry: status=%d, want 403", status)
	}
}

// 面向外部接口：重复操作返回冲突——重复登记候补 409/2011，重复放弃/移除已处理记录 409/2016。
func TestWaitlistAPI_DuplicateOperationsConflict(t *testing.T) {
	env := newWaitlistAPITestEnv(t)

	// 重复登记：u2 首次候补 D 成功，第二次冲突
	status, resp := env.do(http.MethodPost, "/api/v1/plots/"+itoa(env.plotD)+"/waitlist", env.u2Token, map[string]string{})
	if status != http.StatusOK {
		t.Fatalf("first join D: status=%d msg=%s", status, resp.Message)
	}
	status, resp = env.do(http.MethodPost, "/api/v1/plots/"+itoa(env.plotD)+"/waitlist", env.u2Token, map[string]string{})
	if status != http.StatusConflict || resp.Code != constants.CodeWaitlistDuplicate {
		t.Fatalf("duplicate join: status=%d code=%d, want 409/%d", status, resp.Code, constants.CodeWaitlistDuplicate)
	}

	// 重复放弃：B 上的记录已取消，再次放弃冲突
	_, mineResp := env.do(http.MethodGet, "/api/v1/waitlist/mine?status=all&page_size=50", env.u2Token, nil)
	var all struct {
		List []struct {
			ID     uint   `json:"id"`
			Status string `json:"status"`
			PlotID uint   `json:"plot_id"`
		} `json:"list"`
	}
	json.Unmarshal(mineResp.Data, &all)
	var cancelledID uint
	for _, item := range all.List {
		if item.PlotID == env.plotB && item.Status == "cancelled" {
			cancelledID = item.ID
		}
	}
	if cancelledID == 0 {
		t.Fatal("cancelled entry on B not found")
	}
	status, resp = env.do(http.MethodPost, "/api/v1/waitlist/"+itoa(cancelledID)+"/cancel", env.u2Token, nil)
	if status != http.StatusConflict || resp.Code != constants.CodeWaitlistAlreadyProcessed {
		t.Fatalf("duplicate cancel: status=%d code=%d, want 409/%d", status, resp.Code, constants.CodeWaitlistAlreadyProcessed)
	}

	// 管理员重复移除：先移除 A 的受邀队首 u1（顺延给 u2），再次移除同一记录冲突
	var headID uint
	_, headResp := env.do(http.MethodGet, "/api/v1/waitlist?plot_id="+itoa(env.plotA)+"&status=invited", env.adminToken, nil)
	var invited struct {
		List []struct {
			ID     uint `json:"id"`
			UserID uint `json:"user_id"`
		} `json:"list"`
	}
	json.Unmarshal(headResp.Data, &invited)
	for _, item := range invited.List {
		if item.UserID == env.u1ID {
			headID = item.ID
		}
	}
	if headID == 0 {
		t.Fatal("u1 head on A not found")
	}
	status, resp = env.do(http.MethodPost, "/api/v1/waitlist/"+itoa(headID)+"/remove", env.adminToken, map[string]string{"remark": "接口测试移除"})
	if status != http.StatusOK {
		t.Fatalf("first admin remove: status=%d msg=%s", status, resp.Message)
	}
	status, resp = env.do(http.MethodPost, "/api/v1/waitlist/"+itoa(headID)+"/remove", env.adminToken, map[string]string{})
	if status != http.StatusConflict || resp.Code != constants.CodeWaitlistAlreadyProcessed {
		t.Fatalf("duplicate remove: status=%d code=%d, want 409/%d", status, resp.Code, constants.CodeWaitlistAlreadyProcessed)
	}

	// 移除队首后顺延给 u2：A 仍为 pending，u2 处于受邀确认期
	plot, err := env.plotSvc.GetByID(env.plotA)
	if err != nil || plot.Status != string(constants.PlotStatusPending) {
		t.Fatalf("plot A after remove: status=%s err=%v, want pending", plot.Status, err)
	}
}

// 面向外部接口：受邀队首拿到的截止时间必须是带时区的绝对时刻（RFC3339，Z 结尾），
// 且距现在约为确认窗口（30 分钟），与服务器所处时区无关——上海页面据此计算倒计时不会多 8 小时。
func TestWaitlistAPI_DeadlineIsAbsoluteTimestamp(t *testing.T) {
	env := newWaitlistAPITestEnv(t)

	// u1 在地块 A 上已是受邀队首
	status, resp := env.do(http.MethodGet, "/api/v1/waitlist/mine", env.u1Token, nil)
	if status != http.StatusOK {
		t.Fatalf("mine: status=%d msg=%s", status, resp.Message)
	}
	var mine struct {
		List []struct {
			Status           string `json:"status"`
			ConfirmExpiresAt string `json:"confirm_expires_at"`
			RegisteredAt     string `json:"registered_at"`
			RemainSeconds    int64  `json:"remain_seconds"`
		} `json:"list"`
	}
	json.Unmarshal(resp.Data, &mine)
	var invited struct {
		ConfirmExpiresAt string
		RegisteredAt     string
		RemainSeconds    int64
	}
	for _, item := range mine.List {
		if item.Status == "invited" {
			invited.ConfirmExpiresAt = item.ConfirmExpiresAt
			invited.RegisteredAt = item.RegisteredAt
			invited.RemainSeconds = item.RemainSeconds
		}
	}
	if invited.ConfirmExpiresAt == "" {
		t.Fatal("invited head entry not found")
	}
	if !strings.HasSuffix(invited.ConfirmExpiresAt, "Z") {
		t.Fatalf("confirm_expires_at = %q, want RFC3339 UTC with Z suffix", invited.ConfirmExpiresAt)
	}
	deadline, err := time.Parse(time.RFC3339, invited.ConfirmExpiresAt)
	if err != nil {
		t.Fatalf("confirm_expires_at not RFC3339: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, invited.RegisteredAt); err != nil {
		t.Fatalf("registered_at not RFC3339: %v", err)
	}
	d := time.Until(deadline)
	if d < 29*time.Minute || d > 30*time.Minute {
		t.Fatalf("deadline distance = %v, want ~30m (no 8h timezone drift)", d)
	}
	if invited.RemainSeconds <= 0 || invited.RemainSeconds > int64((30*time.Minute).Seconds()) {
		t.Fatalf("remain_seconds = %d, want within (0,1800]", invited.RemainSeconds)
	}
}

// itoa 简单整数转字符串。
func itoa(n uint) string {
	return strconv.FormatUint(uint64(n), 10)
}
