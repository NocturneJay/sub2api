package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// 首单双向奖励的用户端查询（aicat 自研，2026-09）。
// 单独一个文件：user_handler.go 是与上游合并的热点，自研端点不往里挤。

// GetAffiliateFirstOrderBonus 返回当前用户的首充券状态。
// GET /api/v1/user/aff/first-order-bonus
//
// 纯只读：不建 user_affiliates 行、不落首单奖励记录，功能关闭时也照常返回
// 配置字段（前端要用 threshold 之类的数字渲染文案，只是 status=disabled）。
func (h *UserHandler) GetAffiliateFirstOrderBonus(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	status, err := h.affiliateService.GetFirstOrderBonusStatus(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
