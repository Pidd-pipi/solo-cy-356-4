package router

import (
	"testing"

	"github.com/communitygarden/server/internal/config"
	"github.com/communitygarden/server/internal/handler"
	"github.com/communitygarden/server/internal/util"
)

// 冒烟：注册全部路由（含两个 /waitlist 分组与 :id 通配）不应 panic。
func TestBuildSmoke(t *testing.T) {
	cfg := &config.Config{RunMode: "test", JWTSecret: "x", ServerPort: "0"}
	r := New(
		cfg, util.NewLogger("error"), nil,
		(*handler.AuthHandler)(nil), (*handler.UserHandler)(nil),
		(*handler.PlotHandler)(nil), (*handler.WaitlistHandler)(nil),
		(*handler.PlantingPlanHandler)(nil), (*handler.HarvestHandler)(nil),
		(*handler.DiaryHandler)(nil), (*handler.CommunityHandler)(nil),
		(*handler.AuditHandler)(nil), (*handler.StatsHandler)(nil),
		nil, nil,
	)
	engine := r.Build()
	if engine == nil {
		t.Fatal("engine nil")
	}
}
