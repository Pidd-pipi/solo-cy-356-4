package repository

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/util"
)

func ptrTime(t time.Time) *time.Time { return &t }

func seedPlot(t *testing.T, db *gorm.DB, code string) *model.Plot {
	t.Helper()
	p := &model.Plot{Name: code, Code: code, Area: 10, SoilType: "loam", Sunlight: "full", Latitude: 31, Longitude: 121, Status: "adopted"}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed plot: %v", err)
	}
	return p
}

func TestWaitlistRepository_DuplicateAndOrder(t *testing.T) {
	db := newTestDB(t)
	repo := NewWaitlistRepository(db)
	u1 := seedUser(t, db, "w1", "citizen")
	u2 := seedUser(t, db, "w2", "citizen")
	u3 := seedUser(t, db, "w3", "citizen")
	plot := seedPlot(t, db, "WP-1")

	mk := func(uid uint, registered time.Time, status string) *model.WaitlistEntry {
		e := &model.WaitlistEntry{PlotID: plot.ID, UserID: uid, Status: status, RegisteredAt: registered}
		e.RefreshActiveKey()
		return e
	}
	base := time.Now()
	if err := repo.Create(nil, mk(u1.ID, base, "waiting")); err != nil {
		t.Fatalf("create e1: %v", err)
	}
	if err := repo.Create(nil, mk(u2.ID, base.Add(time.Second), "waiting")); err != nil {
		t.Fatalf("create e2: %v", err)
	}
	// 同一人同一地块第二条有效记录 → 唯一约束冲突
	dup := mk(u1.ID, base.Add(2*time.Second), "waiting")
	if err := repo.Create(nil, dup); err != ErrWaitlistDuplicate {
		t.Fatalf("duplicate active entry err = %v, want ErrWaitlistDuplicate", err)
	}
	// 队首按登记时间最早
	if err := db.Transaction(func(tx *gorm.DB) error {
		head, err := repo.HeadForUpdate(tx, plot.ID)
		if err != nil {
			return err
		}
		if head.UserID != u1.ID {
			t.Errorf("head user = %d, want %d", head.UserID, u1.ID)
		}
		// invited 优先于更早的 waiting
		e3 := mk(u3.ID, base.Add(-time.Minute), "invited")
		e3.InvitedAt = &base
		e3.ConfirmExpiresAt = ptrTime(base.Add(time.Hour))
		if err := repo.Create(tx, e3); err != nil {
			return err
		}
		head2, err := repo.HeadForUpdate(tx, plot.ID)
		if err != nil {
			return err
		}
		if head2.UserID != u3.ID {
			t.Errorf("head after invite user = %d, want invited u3 %d", head2.UserID, u3.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}

	// 排队位置：u2 前面有 u1(waiting) 与 u3(invited) 两人
	e2, err := repo.FindActive(plot.ID, u2.ID)
	if err != nil {
		t.Fatalf("find active u2: %v", err)
	}
	ahead, err := repo.CountAhead(nil, e2)
	if err != nil || ahead != 2 {
		t.Fatalf("u2 ahead = %d err=%v, want 2", ahead, err)
	}

	// 终态后释放唯一占位，可重新候补
	e1, _ := repo.FindActive(plot.ID, u1.ID)
	e1.MarkTerminal("cancelled", base)
	if err := repo.Update(nil, e1); err != nil {
		t.Fatalf("cancel e1: %v", err)
	}
	if err := repo.Create(nil, mk(u1.ID, base.Add(3*time.Second), "waiting")); err != nil {
		t.Fatalf("re-join after terminal: %v", err)
	}

	// 我的候补 / 地块队列列表
	if _, total, err := repo.ListByUser(u1.ID, "", util.PageQuery{Page: 1, PageSize: 10}); err != nil || total != 1 {
		t.Fatalf("list by user total=%d err=%v", total, err)
	}
	// 地块队列列表（管理端默认含终态历史：e1 已取消 + e1 重新候补 + e2 + e3 = 4）
	if _, total, err := repo.ListByPlot(plot.ID, "", util.PageQuery{Page: 1, PageSize: 10}); err != nil || total != 4 {
		t.Fatalf("list by plot total=%d err=%v", total, err)
	}
	if _, total, err := repo.ListByPlot(plot.ID, "waiting", util.PageQuery{Page: 1, PageSize: 10}); err != nil || total != 2 {
		t.Fatalf("list by plot waiting total=%d err=%v, want 2", total, err)
	}
}

func TestWaitlistRepository_ExpiredQuery(t *testing.T) {
	db := newTestDB(t)
	repo := NewWaitlistRepository(db)
	u := seedUser(t, db, "wx", "citizen")
	plot := seedPlot(t, db, "WP-2")
	now := time.Now()
	expired := &model.WaitlistEntry{PlotID: plot.ID, UserID: u.ID, Status: string(constants.WaitlistInvited), RegisteredAt: now, ConfirmExpiresAt: ptrTime(now.Add(-time.Minute))}
	expired.RefreshActiveKey()
	if err := repo.Create(nil, expired); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListExpiredInvited(now, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("expired list len=%d err=%v", len(list), err)
	}
}
