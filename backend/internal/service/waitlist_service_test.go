package service

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// newWaitlistServices 构造联动的地块/候补服务（释放自动触发递补）。
func newWaitlistServices(t *testing.T, db *gorm.DB, confirmMinutes int) (*PlotService, *WaitlistService) {
	t.Helper()
	plotRepo := repository.NewPlotRepository(db)
	waitRepo := repository.NewWaitlistRepository(db)
	plotSvc := NewPlotService(plotRepo, db, testLogger())
	waitSvc := NewWaitlistService(waitRepo, plotRepo, db, testLogger(), confirmMinutes)
	plotSvc.SetWaitlistPromoter(waitSvc)
	return plotSvc, waitSvc
}

func appErrorCode(t *testing.T, err error) int {
	t.Helper()
	var ae *util.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	t.Fatalf("expected *util.AppError, got %T: %v", err, err)
	return 0
}

// backdateInvitation 直接把 invited 记录的确认截止时间调到过去（模拟逾期）。
func backdateInvitation(t *testing.T, db *gorm.DB, entryID uint) {
	t.Helper()
	if err := db.Model(&model.WaitlistEntry{}).Where("id = ?", entryID).
		Update("confirm_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("backdate invitation: %v", err)
	}
}

// 场景一：正常认养不受候补影响（available -> adopted，重复认养冲突）。
func TestWaitlist_NormalAdoptUnaffected(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, _ := newWaitlistServices(t, db, 30)
	user := newTestUser(t, db, "u-normal", "citizen")
	plot := newTestPlot(t, db, "P-W-NORMAL", "available", nil)

	got, err := plotSvc.Adopt(plot.ID, user.ID, "citizen", user.Username)
	if err != nil {
		t.Fatalf("adopt available plot: %v", err)
	}
	if got.Status != "adopted" || got.AdopterID == nil || *got.AdopterID != user.ID {
		t.Fatalf("unexpected adopt result: %+v", got)
	}
	if _, err := plotSvc.Adopt(plot.ID, user.ID, "citizen", user.Username); err == nil {
		t.Fatalf("second adopt should conflict")
	}
}

// 场景：无候补队列时释放，地块按原流程直接回到 available。
func TestWaitlist_ReleaseWithoutQueueUnaffected(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, _ := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-rel", "farmer")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-RELEMPTY", "harvested", &oid)

	got, err := plotSvc.Release(plot.ID, owner.ID, "farmer")
	if err != nil {
		t.Fatalf("release without queue: %v", err)
	}
	if got.Status != "available" || got.AdopterID != nil {
		t.Fatalf("plot should return to available, got status=%s adopter=%v", got.Status, got.AdopterID)
	}
}

// 场景二：重复候补被拒绝；排队位置按登记时间；只有 adopted/harvested 可候补。
func TestWaitlist_JoinDuplicateAndPosition(t *testing.T) {
	db := newTestServiceDB(t)
	_, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-join", "farmer")
	b := newTestUser(t, db, "wb", "citizen")
	c := newTestUser(t, db, "wc", "citizen")
	d := newTestUser(t, db, "wd", "citizen")
	oid := owner.ID
	adopted := newTestPlot(t, db, "P-W-JOIN", "adopted", &oid)
	available := newTestPlot(t, db, "P-W-JOIN-AV", "available", nil)

	vb, err := waitSvc.Join(adopted.ID, b.ID, "citizen", b.Username, "")
	if err != nil {
		t.Fatalf("b join: %v", err)
	}
	if vb.Position != 1 {
		t.Errorf("b position = %d, want 1", vb.Position)
	}
	vc, err := waitSvc.Join(adopted.ID, c.ID, "citizen", c.Username, "")
	if err != nil || vc.Position != 2 {
		t.Fatalf("c join pos=%d err=%v", vc.Position, err)
	}
	vd, err := waitSvc.Join(adopted.ID, d.ID, "citizen", d.Username, "")
	if err != nil || vd.Position != 3 {
		t.Fatalf("d join pos=%d err=%v", vd.Position, err)
	}
	// 同一人同一地块只能一条有效记录
	if _, err := waitSvc.Join(adopted.ID, b.ID, "citizen", b.Username, ""); err == nil {
		t.Fatalf("duplicate join should fail")
	} else if appErrorCode(t, err) != constants.CodeWaitlistDuplicate {
		t.Fatalf("duplicate join code = %d, want %d", appErrorCode(t, err), constants.CodeWaitlistDuplicate)
	}
	// 空闲地块不可候补（可直接认养）
	if _, err := waitSvc.Join(available.ID, b.ID, "citizen", b.Username, ""); err == nil {
		t.Fatalf("join available plot should fail")
	} else if appErrorCode(t, err) != constants.CodeWaitlistNotAllowed {
		t.Fatalf("join available code = %d", appErrorCode(t, err))
	}
	// 认养人不能候补自己的地块
	if _, err := waitSvc.Join(adopted.ID, owner.ID, "farmer", owner.Username, ""); err == nil {
		t.Fatalf("owner self-join should fail")
	}
}

// 场景三：释放后队首受邀，确认期内他人不可认养；队首在限定时间内确认认养。
func TestWaitlist_ReleaseInvitesHeadAndHeadConfirms(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-conf", "farmer")
	b := newTestUser(t, db, "cb", "citizen")
	c := newTestUser(t, db, "cc", "citizen")
	other := newTestUser(t, db, "cother", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-CONF", "harvested", &oid)

	if _, err := waitSvc.Join(plot.ID, b.ID, "citizen", b.Username, ""); err != nil {
		t.Fatalf("b join: %v", err)
	}
	if _, err := waitSvc.Join(plot.ID, c.ID, "citizen", c.Username, ""); err != nil {
		t.Fatalf("c join: %v", err)
	}
	released, err := plotSvc.Release(plot.ID, owner.ID, "farmer")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.Status != "pending" || released.AdopterID != nil {
		t.Fatalf("after release with queue plot should be pending without adopter, got %s", released.Status)
	}

	views, _, err := waitSvc.ListMine(b.ID, "", util.PageQuery{Page: 1, PageSize: 10})
	if err != nil || len(views) != 1 {
		t.Fatalf("list mine b: len=%d err=%v", len(views), err)
	}
	head := views[0].Entry
	if head.Status != "invited" || head.ConfirmExpiresAt == nil {
		t.Fatalf("b should be invited with deadline: %+v", head)
	}
	if views[0].Position != 1 {
		t.Errorf("invited head position = %d, want 1", views[0].Position)
	}
	// 确认期间不可被他人认养
	if _, err := plotSvc.Adopt(plot.ID, other.ID, "citizen", other.Username); err == nil {
		t.Fatalf("adopt during pending must fail")
	} else if appErrorCode(t, err) != constants.CodePlotConfirming {
		t.Fatalf("adopt pending code = %d, want %d", appErrorCode(t, err), constants.CodePlotConfirming)
	}
	// 非受邀人确认拒绝
	if _, _, err := waitSvc.Confirm(head.ID, c.ID, "citizen", c.Username); err == nil {
		t.Fatalf("non-head confirm must fail")
	}
	// 队首确认认养
	entry, adoptedPlot, err := waitSvc.Confirm(head.ID, b.ID, "citizen", b.Username)
	if err != nil {
		t.Fatalf("head confirm: %v", err)
	}
	if entry.Status != "confirmed" {
		t.Errorf("entry status = %s", entry.Status)
	}
	if adoptedPlot.Status != "adopted" || adoptedPlot.AdopterID == nil || *adoptedPlot.AdopterID != b.ID {
		t.Fatalf("plot after confirm: status=%s adopter=%v", adoptedPlot.Status, adoptedPlot.AdopterID)
	}
	// 队列中后一位仍在排队，此时位置为 1
	cViews, _, _ := waitSvc.ListMine(c.ID, "", util.PageQuery{Page: 1, PageSize: 10})
	if len(cViews) != 1 || cViews[0].Entry.Status != "waiting" || cViews[0].Position != 1 {
		t.Fatalf("c should remain waiting at position 1, got %+v", cViews)
	}
}

// 非队首（waiting）调用确认 → 拒绝。
func TestWaitlist_ConfirmWhileWaitingRejected(t *testing.T) {
	db := newTestServiceDB(t)
	_, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-waiting", "farmer")
	b := newTestUser(t, db, "wb2", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-WAITING", "adopted", &oid)
	v, err := waitSvc.Join(plot.ID, b.ID, "citizen", b.Username, "")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, _, err := waitSvc.Confirm(v.Entry.ID, b.ID, "citizen", b.Username); err == nil {
		t.Fatalf("confirm while waiting must fail")
	} else if appErrorCode(t, err) != constants.CodeWaitlistNotInvited {
		t.Fatalf("code = %d, want %d", appErrorCode(t, err), constants.CodeWaitlistNotInvited)
	}
}

// 场景四：逾期自动顺延——后台扫描与确认接口惰性顺延两条路径。
func TestWaitlist_OverdueSweepAdvances(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-sweep", "farmer")
	b := newTestUser(t, db, "sb", "citizen")
	c := newTestUser(t, db, "sc", "citizen")
	d := newTestUser(t, db, "sd", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-SWEEP", "harvested", &oid)
	for _, u := range []*model.User{b, c, d} {
		if _, err := waitSvc.Join(plot.ID, u.ID, "citizen", u.Username, ""); err != nil {
			t.Fatalf("join: %v", err)
		}
	}
	if _, err := plotSvc.Release(plot.ID, owner.ID, "farmer"); err != nil {
		t.Fatalf("release: %v", err)
	}

	first := mustInvitedHead(t, waitSvc, b.ID)
	// B 逾期 → 自动顺延给 C
	backdateInvitation(t, db, first.ID)
	n, err := waitSvc.SweepOverdue()
	if err != nil || n != 1 {
		t.Fatalf("sweep n=%d err=%v", n, err)
	}
	assertEntryStatus(t, waitSvc, first.ID, "expired")
	second := mustInvitedHead(t, waitSvc, c.ID)
	assertPlotStatus(t, plotSvc, plot.ID, "pending")

	// C 逾期 → 顺延给 D
	backdateInvitation(t, db, second.ID)
	if n, err := waitSvc.SweepOverdue(); err != nil || n != 1 {
		t.Fatalf("sweep2 n=%d err=%v", n, err)
	}
	assertEntryStatus(t, waitSvc, second.ID, "expired")
	third := mustInvitedHead(t, waitSvc, d.ID)

	// D 逾期且无后续候选 → 地块回到空闲池
	backdateInvitation(t, db, third.ID)
	if n, err := waitSvc.SweepOverdue(); err != nil || n != 1 {
		t.Fatalf("sweep3 n=%d err=%v", n, err)
	}
	assertEntryStatus(t, waitSvc, third.ID, "expired")
	assertPlotStatus(t, plotSvc, plot.ID, "available")
}

// 场景四（惰性顺延）：队首逾期后本人调用确认，接口完成顺延后返回“已逾期”，下一位可确认。
func TestWaitlist_OverdueLazyConfirmAdvances(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-lazy", "farmer")
	b := newTestUser(t, db, "lb", "citizen")
	c := newTestUser(t, db, "lc", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-LAZY", "harvested", &oid)
	if _, err := waitSvc.Join(plot.ID, b.ID, "citizen", b.Username, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := waitSvc.Join(plot.ID, c.ID, "citizen", c.Username, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := plotSvc.Release(plot.ID, owner.ID, "farmer"); err != nil {
		t.Fatal(err)
	}
	first := mustInvitedHead(t, waitSvc, b.ID)
	backdateInvitation(t, db, first.ID)

	if _, _, err := waitSvc.Confirm(first.ID, b.ID, "citizen", b.Username); err == nil {
		t.Fatalf("overdue confirm must return error")
	} else if appErrorCode(t, err) != constants.CodeWaitlistConfirmExpired {
		t.Fatalf("overdue confirm code = %d", appErrorCode(t, err))
	}
	// 顺延已随事务提交：B expired，C invited
	assertEntryStatus(t, waitSvc, first.ID, "expired")
	next := mustInvitedHead(t, waitSvc, c.ID)
	if _, adoptedPlot, err := waitSvc.Confirm(next.ID, c.ID, "citizen", c.Username); err != nil {
		t.Fatalf("next head confirm: %v", err)
	} else if adoptedPlot.AdopterID == nil || *adoptedPlot.AdopterID != c.ID {
		t.Fatalf("plot should be adopted by c")
	}
}

// 场景五：主动放弃（排队中放弃可重新候补；确认期放弃自动顺延下一位）。
func TestWaitlist_CancelWaitingAndInvited(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-cancel", "farmer")
	b := newTestUser(t, db, "xb", "citizen")
	c := newTestUser(t, db, "xc", "citizen")
	oid := owner.ID

	// 排队中放弃
	adopted := newTestPlot(t, db, "P-W-CAN1", "adopted", &oid)
	vb, _ := waitSvc.Join(adopted.ID, b.ID, "citizen", b.Username, "")
	waitSvc.Join(adopted.ID, c.ID, "citizen", c.Username, "")
	if _, err := waitSvc.Cancel(vb.Entry.ID, b.ID, "citizen", b.Username); err != nil {
		t.Fatalf("cancel waiting: %v", err)
	}
	assertEntryStatus(t, waitSvc, vb.Entry.ID, "cancelled")
	// 终态后可重新登记候补
	if _, err := waitSvc.Join(adopted.ID, b.ID, "citizen", b.Username, ""); err != nil {
		t.Fatalf("re-join after cancel: %v", err)
	}
	// 已处理记录不能重复放弃
	if _, err := waitSvc.Cancel(vb.Entry.ID, b.ID, "citizen", b.Username); err == nil {
		t.Fatalf("cancel terminal entry must fail")
	}

	// 确认期放弃 → 自动顺延下一位
	harvested := newTestPlot(t, db, "P-W-CAN2", "harvested", &oid)
	y1 := newTestUser(t, db, "y1", "citizen")
	y2 := newTestUser(t, db, "y2", "citizen")
	waitSvc.Join(harvested.ID, y1.ID, "citizen", y1.Username, "")
	waitSvc.Join(harvested.ID, y2.ID, "citizen", y2.Username, "")
	plotSvc.Release(harvested.ID, owner.ID, "farmer")
	head := mustInvitedHead(t, waitSvc, y1.ID)
	if _, err := waitSvc.Cancel(head.ID, y1.ID, "citizen", y1.Username); err != nil {
		t.Fatalf("cancel invited: %v", err)
	}
	assertEntryStatus(t, waitSvc, head.ID, "cancelled")
	next := mustInvitedHead(t, waitSvc, y2.ID)
	if _, plot, err := waitSvc.Confirm(next.ID, y2.ID, "citizen", y2.Username); err != nil || plot.AdopterID == nil || *plot.AdopterID != y2.ID {
		t.Fatalf("advanced head confirm: err=%v plot=%v", err, plot)
	}
}

// 管理端：移除候选（队首移除触发顺延）与处理逾期异常。
func TestWaitlist_AdminRemoveAndExpire(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, waitSvc := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-admin", "farmer")
	admin := newTestUser(t, db, "admin-w", "admin")
	b := newTestUser(t, db, "ab", "citizen")
	c := newTestUser(t, db, "ac", "citizen")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-ADMIN", "harvested", &oid)
	waitSvc.Join(plot.ID, b.ID, "citizen", b.Username, "")
	waitSvc.Join(plot.ID, c.ID, "citizen", c.Username, "")
	plotSvc.Release(plot.ID, owner.ID, "farmer")

	head := mustInvitedHead(t, waitSvc, b.ID)
	if _, err := waitSvc.AdminRemove(head.ID, admin.ID, "admin", "资料异常移除"); err != nil {
		t.Fatalf("admin remove: %v", err)
	}
	assertEntryStatus(t, waitSvc, head.ID, "removed")
	next := mustInvitedHead(t, waitSvc, c.ID)

	// 管理端处理逾期异常：C 被置过期且无后续 → 地块回到空闲池
	backdateInvitation(t, db, next.ID)
	entry, advanced, err := waitSvc.AdminExpire(next.ID, admin.ID, "admin")
	if err != nil {
		t.Fatalf("admin expire: %v", err)
	}
	if entry.Status != "expired" || advanced != nil {
		t.Fatalf("admin expire result: status=%s advanced=%v", entry.Status, advanced)
	}
	assertPlotStatus(t, plotSvc, plot.ID, "available")

	// 管理端队列查看
	views, total, err := waitSvc.ListAll(plot.ID, "", util.PageQuery{Page: 1, PageSize: 10})
	if err != nil || total != 2 || len(views) != 2 {
		t.Fatalf("admin list: total=%d len=%d err=%v", total, len(views), err)
	}
}

// 原流程不受影响：种植计划完成路径调用 MarkHarvested 将地块置为 harvested。
func TestWaitlist_PlanCompletionMarkHarvestedUnaffected(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, _ := newWaitlistServices(t, db, 30)
	owner := newTestUser(t, db, "owner-mark", "farmer")
	oid := owner.ID
	plot := newTestPlot(t, db, "P-W-MARK", "adopted", &oid)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return plotSvc.MarkHarvested(tx, plot.ID)
	}); err != nil {
		t.Fatalf("MarkHarvested: %v", err)
	}
	got, err := plotSvc.GetByID(plot.ID)
	if err != nil || got.Status != "harvested" {
		t.Fatalf("mark harvested path broken: status=%s err=%v", got.Status, err)
	}
}

func mustInvitedHead(t *testing.T, waitSvc *WaitlistService, userID uint) *model.WaitlistEntry {
	t.Helper()
	views, _, err := waitSvc.ListMine(userID, "invited", util.PageQuery{Page: 1, PageSize: 10})
	if err != nil || len(views) != 1 {
		t.Fatalf("expected one invited entry for user %d: len=%d err=%v", userID, len(views), err)
	}
	e := views[0].Entry
	if e.Status != "invited" || e.ConfirmExpiresAt == nil {
		t.Fatalf("entry %d not invited: %+v", e.ID, e)
	}
	return &e
}

func assertEntryStatus(t *testing.T, waitSvc *WaitlistService, id uint, want string) {
	t.Helper()
	e, err := waitSvc.GetByID(id)
	if err != nil {
		t.Fatalf("get entry %d: %v", id, err)
	}
	if e.Status != want {
		t.Fatalf("entry %d status = %s, want %s", id, e.Status, want)
	}
}

func assertPlotStatus(t *testing.T, plotSvc *PlotService, id uint, want string) {
	t.Helper()
	p, err := plotSvc.GetByID(id)
	if err != nil {
		t.Fatalf("get plot %d: %v", id, err)
	}
	if p.Status != want {
		t.Fatalf("plot %d status = %s, want %s", id, p.Status, want)
	}
}
