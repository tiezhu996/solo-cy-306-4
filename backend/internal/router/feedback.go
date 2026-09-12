package router

import (
	"gbevent/internal/constants"
	"gbevent/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerFeedbackRoutes 反馈问卷路由。
func (r *Router) registerFeedbackRoutes(g *gin.RouterGroup) {
	feedback := g.Group("/activities/:id/feedback")
	feedback.Use(middleware.AuthRequired(r.cfg))
	// 参加者：查看问卷与提交（业务层校验活动已发布问卷且本人已签到）。
	feedback.GET("", r.feedback.Detail)
	feedback.POST("/submit", r.feedback.Submit)

	// 组织者/管理员：创建、发布与统计。
	organizerOnly := middleware.RequireRole(constants.RoleOrganizer, constants.RoleAdmin)
	feedback.POST("", organizerOnly, r.feedback.Create)
	feedback.POST("/publish", organizerOnly, r.feedback.Publish)
	feedback.GET("/stats", organizerOnly, r.feedback.Stats)
}
