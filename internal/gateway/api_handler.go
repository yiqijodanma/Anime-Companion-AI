package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type chatReq struct {
	OpenID string `json:"open_id"`
	Text   string `json:"text"`
}

func (h *Handlers) registerAPI(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	v1.POST("/chat", h.apiChat)
	v1.GET("/conversations/:open_id/messages", h.apiListMessages)
	v1.DELETE("/conversations/:open_id/messages", h.apiDeleteMessages)
	r.GET("/healthz", h.healthz)
}

func apiError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

func apiAgentError(c *gin.Context, err error) {
	if grpcstatus.Code(err) == codes.InvalidArgument {
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	apiError(c, http.StatusBadGateway, "agent_error", err.Error())
}

func (h *Handlers) apiChat(c *gin.Context) {
	var req chatReq
	if err := c.ShouldBindJSON(&req); err != nil || req.OpenID == "" || req.Text == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "open_id 和 text 必填")
		return
	}
	if !h.allowOpenID(c.Request.Context(), req.OpenID, "api_chat") {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	reply, err := h.Agent.Reply(c.Request.Context(), req.OpenID, req.Text)
	if err != nil {
		apiError(c, http.StatusBadGateway, "agent_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply})
}

func (h *Handlers) apiListMessages(c *gin.Context) {
	openID := c.Param("open_id")
	if openID == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "open_id 必填")
		return
	}
	messages, err := h.Agent.ListMessages(c.Request.Context(), openID)
	if err != nil {
		apiAgentError(c, err)
		return
	}
	if messages == nil {
		messages = []ConversationMessage{}
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handlers) apiDeleteMessages(c *gin.Context) {
	openID := c.Param("open_id")
	if openID == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "open_id 必填")
		return
	}
	if err := h.Agent.DeleteMessages(c.Request.Context(), openID); err != nil {
		apiAgentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handlers) healthz(c *gin.Context) {
	if err := h.Agent.Check(c.Request.Context()); err != nil {
		apiError(c, http.StatusServiceUnavailable, "agent_unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
