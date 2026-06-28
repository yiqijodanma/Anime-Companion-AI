package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type chatReq struct {
	OpenID string `json:"open_id"`
	Text   string `json:"text"`
}

func (h *Handlers) registerAPI(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	v1.POST("/chat", h.apiChat)
	r.GET("/healthz", h.healthz)
}

func apiError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

func (h *Handlers) apiChat(c *gin.Context) {
	var req chatReq
	if err := c.ShouldBindJSON(&req); err != nil || req.OpenID == "" || req.Text == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "open_id 和 text 必填")
		return
	}
	reply, err := h.Agent.Reply(c.Request.Context(), req.OpenID, req.Text)
	if err != nil {
		apiError(c, http.StatusBadGateway, "agent_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply})
}

func (h *Handlers) healthz(c *gin.Context) {
	if err := h.Agent.Check(c.Request.Context()); err != nil {
		apiError(c, http.StatusServiceUnavailable, "agent_unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
