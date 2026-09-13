package router

import (
	"github.com/gin-gonic/gin"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/middleware"
)

// registerWaitlist 地块候补与递补路由。
func (r *Router) registerWaitlist(g *gin.RouterGroup) {
	// 我的候补 / 确认 / 放弃：登录用户
	auth := g.Group("/waitlist")
	auth.Use(middleware.Auth(r.cfg, r.logger))
	{
		auth.GET("/mine", r.waitlistHandler.ListMine)
		auth.POST("/:id/confirm", r.waitlistHandler.Confirm)
		auth.POST("/:id/cancel", r.waitlistHandler.Cancel)
	}

	// 管理端：查看队列 / 移除候选 / 处理逾期异常
	admin := g.Group("/waitlist")
	admin.Use(middleware.Auth(r.cfg, r.logger), middleware.RequireRoles(string(constants.RoleAdmin)))
	{
		admin.GET("", r.waitlistHandler.ListAll)
		admin.POST("/:id/remove", r.waitlistHandler.AdminRemove)
		admin.POST("/:id/expire", r.waitlistHandler.AdminExpire)
	}
}
