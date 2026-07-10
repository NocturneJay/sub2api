package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetModelPlazaMeta returns display-only endpoint labels for the model plaza.
func (h *SettingHandler) GetModelPlazaMeta(c *gin.Context) {
	meta, err := h.settingService.GetModelPlazaMeta(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, meta)
}

type UpdateModelPlazaMetaRequest struct {
	Models map[string]service.ModelPlazaModelMeta `json:"models"`
}

// UpdateModelPlazaMeta updates display metadata only; it does not affect billing or routing.
func (h *SettingHandler) UpdateModelPlazaMeta(c *gin.Context) {
	var req UpdateModelPlazaMetaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.settingService.SetModelPlazaMeta(c.Request.Context(), &service.ModelPlazaMeta{Models: req.Models}); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	meta, err := h.settingService.GetModelPlazaMeta(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, meta)
}
