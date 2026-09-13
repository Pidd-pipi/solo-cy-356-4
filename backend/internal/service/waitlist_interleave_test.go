package service

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/util"
)

// installPromotionHook 安装测试接缝：在 Cancel/AdminRemove 取得地块锁后、
// 重读候补行前，把目标记录模拟为“释放事务刚把本人由 waiting 提升为 invited”，
// 同时把地块置为 pending——即确定性复现交错时序：
// 本人预读到 waiting → 地块恰好释放（本人成队首）→ 放弃/移除在锁内看到 invited。
func installPromotionHook(t *testing.T, targetID uint, ttl time.Duration) {
	t.Helper()
	hookErr := func(format string, args ...any) { t.Fatalf(format, args...) }
	waitlistPreLockHook = func(tx *gorm.DB, entryID uint) {
		if entryID != targetID {
			return
		}
		var e model.WaitlistEntry
		if err := tx.First(&e, entryID).Error; err != nil {
			hookErr("hook load entry: %v", err)
		}
		now := time.Now()
		if err := tx.Model(&model.WaitlistEntry{}).Where("id = ?", entryID).
			Updates(map[string]any{"status": "invited", "invited_at": now, "confirm_expires_at": now.Add(ttl)}).Error; err != nil {
			hookErr("hook promote entry: %v", err)
		}
		if err := tx.Model(&model.Plot{}).Where("id = ?", e.PlotID).
			Update("status", "pending").Error; err != nil {
			hookErr("hook set plot pending: %v", err)
		}
	}
	t.Cleanup(func() { waitlistPreLockHook = nil })
}

// 交错一：候补还在排队时地块释放，本人随后主动放弃——
// 放弃时必须按锁内最新状态（已被提升为队首）顺延给下一位，地块不得停在 pending。
func TestWaitlist_StalePreReadCancelAdvances(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-ix-cancel", "farmer")
	a := newTestUser(t, db, "ixa", "citizen")
	b := newTestUser(t, db, "ixb", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-IX-CANCEL", "harvested", &oid)

	va, _ := waitSvc.Join(plot.ID, a.ID, "citizen", a.Username, "")
	waitSvc.Join(plot.ID, b.ID, "citizen", b.Username, "")

	installPromotionHook(t, va.Entry.ID, 30*time.Minute)
	if _, err := waitSvc.Cancel(va.Entry.ID, a.ID, "citizen", a.Username); err != nil {
		t.Fatalf("cancel during interleave: %v", err)
	}
	assertEntryStatus(t, waitSvc, va.Entry.ID, "cancelled")
	next := mustInvitedHead(t, waitSvc, b.ID)
	assertPlotStatus(t, plotSvc, plot.ID, "pending")

	// 下一位 B 可正常完成确认，地块认养到 B 名下
	_, adoptedPlot, err := waitSvc.Confirm(next.ID, b.ID, "citizen", b.Username)
	if err != nil || adoptedPlot.AdopterID == nil || *adoptedPlot.AdopterID != b.ID {
		t.Fatalf("next head confirm after interleave cancel: err=%v plot=%v", err, adoptedPlot)
	}
}

// 交错二：本人是队首（被释放提升）且队列无下一位，主动放弃后地块必须回到空闲池。
func TestWaitlist_StalePreReadCancelLastHeadBackToAvailable(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-ix-last", "farmer")
	a := newTestUser(t, db, "ixlasta", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-IX-LAST", "harvested", &oid)

	va, _ := waitSvc.Join(plot.ID, a.ID, "citizen", a.Username, "")
	installPromotionHook(t, va.Entry.ID, 30*time.Minute)
	if _, err := waitSvc.Cancel(va.Entry.ID, a.ID, "citizen", a.Username); err != nil {
		t.Fatalf("cancel last head: %v", err)
	}
	assertEntryStatus(t, waitSvc, va.Entry.ID, "cancelled")
	assertPlotStatus(t, plotSvc, plot.ID, "available")
}

// 交错三：管理员移除时记录恰好被释放流程提升为队首——必须顺延给下一位。
func TestWaitlist_StalePreReadAdminRemoveAdvances(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-ix-rm", "farmer")
	admin := newTestUser(t, db, "admin-ix", "admin")
	a := newTestUser(t, db, "ixrma", "citizen")
	b := newTestUser(t, db, "ixrmb", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-IX-RM", "harvested", &oid)

	va, _ := waitSvc.Join(plot.ID, a.ID, "citizen", a.Username, "")
	waitSvc.Join(plot.ID, b.ID, "citizen", b.Username, "")

	installPromotionHook(t, va.Entry.ID, 30*time.Minute)
	if _, err := waitSvc.AdminRemove(va.Entry.ID, admin.ID, "admin", "交错移除"); err != nil {
		t.Fatalf("admin remove during interleave: %v", err)
	}
	assertEntryStatus(t, waitSvc, va.Entry.ID, "removed")
	mustInvitedHead(t, waitSvc, b.ID)
	assertPlotStatus(t, plotSvc, plot.ID, "pending")
}

// 问题一：我的候补切到“全部记录”时，应包含本人已取消、已逾期、已移除的历史；
// 默认仅返回有效记录，状态过滤也仍然可用。
func TestWaitlist_MineAllIncludesTerminalHistory(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-history", "farmer")
	admin := newTestUser(t, db, "admin-history", "admin")
	oid := owner.ID

	// 本人 1：已取消（排队中直接放弃）
	u := newTestUser(t, db, "hist-u", "citizen")
	p1 := newTestPlot(t, db, "P-HIST-1", "adopted", &oid)
	cancelled, _ := waitSvc.Join(p1.ID, u.ID, "citizen", u.Username, "")
	if _, err := waitSvc.Cancel(cancelled.Entry.ID, u.ID, "citizen", u.Username); err != nil {
		t.Fatal(err)
	}

	// 本人 2：已逾期（受邀后不确认，后台扫描顺延）
	p2 := newTestPlot(t, db, "P-HIST-2", "harvested", &oid)
	if _, err := waitSvc.Join(p2.ID, u.ID, "citizen", u.Username, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := plotSvc.Release(p2.ID, owner.ID, "farmer"); err != nil {
		t.Fatal(err)
	}
	head := mustInvitedHead(t, waitSvc, u.ID)
	backdateInvitation(t, db, head.ID)
	if n, err := waitSvc.SweepOverdue(); err != nil || n != 1 {
		t.Fatalf("sweep n=%d err=%v", n, err)
	}

	// 本人 3：被管理员移除（受邀队首被移除，地块回空闲）
	p3 := newTestPlot(t, db, "P-HIST-3", "harvested", &oid)
	if _, err := waitSvc.Join(p3.ID, u.ID, "citizen", u.Username, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := plotSvc.Release(p3.ID, owner.ID, "farmer"); err != nil {
		t.Fatal(err)
	}
	head3 := mustInvitedHead(t, waitSvc, u.ID)
	if _, err := waitSvc.AdminRemove(head3.ID, admin.ID, "admin", "测试移除"); err != nil {
		t.Fatal(err)
	}

	// 本人 4：仍在排队的有效记录
	p4 := newTestPlot(t, db, "P-HIST-4", "adopted", &oid)
	if _, err := waitSvc.Join(p4.ID, u.ID, "citizen", u.Username, ""); err != nil {
		t.Fatal(err)
	}

	pq := util.PageQuery{Page: 1, PageSize: 50}
	active, activeTotal, err := waitSvc.ListMine(u.ID, "", pq)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range active {
		if !v.Entry.IsActive() {
			t.Errorf("default mine list leaked terminal record id=%d status=%s", v.Entry.ID, v.Entry.Status)
		}
	}
	if activeTotal != 1 {
		t.Fatalf("active total = %d, want 1", activeTotal)
	}

	all, allTotal, err := waitSvc.ListMine(u.ID, "all", pq)
	if err != nil {
		t.Fatal(err)
	}
	if allTotal != 4 {
		t.Fatalf("all history total = %d, want 4", allTotal)
	}
	gotStatus := map[string]bool{}
	for _, v := range all {
		gotStatus[v.Entry.Status] = true
	}
	for _, want := range []string{"cancelled", "expired", "removed", "waiting"} {
		if !gotStatus[want] {
			t.Errorf("all history missing status %s, got=%v", want, gotStatus)
		}
	}

	// 单一终态状态过滤仍可用
	expiredOnly, expiredTotal, err := waitSvc.ListMine(u.ID, "expired", pq)
	if err != nil || expiredTotal != 1 || len(expiredOnly) != 1 {
		t.Fatalf("expired filter total=%d len=%d err=%v, want 1", expiredTotal, len(expiredOnly), err)
	}
}
