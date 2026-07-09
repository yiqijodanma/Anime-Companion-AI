package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"companion-ai/internal/conversation"
)

type chatReq struct {
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
	Text       string `json:"text"`
}

func (h *Handlers) registerAPI(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	v1.POST("/chat", h.apiChat)
	v1.GET("/conversations/:channel/:external_id/messages", h.apiListMessages)
	v1.DELETE("/conversations/:channel/:external_id/messages", h.apiDeleteMessages)
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
	if err := c.ShouldBindJSON(&req); err != nil || req.Channel == "" || req.ExternalID == "" || req.Text == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "channel、external_id 和 text 必填")
		return
	}
	if !allowedAPIChannel(req.Channel) {
		apiError(c, http.StatusBadRequest, "invalid_request", "unsupported channel")
		return
	}
	if !h.allowOpenID(c.Request.Context(), req.ExternalID, "api_chat") {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	reply, err := h.Agent.Reply(c.Request.Context(), req.Channel, req.ExternalID, req.Text)
	if err != nil {
		apiError(c, http.StatusBadGateway, "agent_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply})
}

func (h *Handlers) apiListMessages(c *gin.Context) {
	channel, externalID, ok := conversationParams(c)
	if !ok {
		apiError(c, http.StatusBadRequest, "invalid_request", "channel 和 external_id 必填")
		return
	}
	messages, err := h.Agent.ListMessages(c.Request.Context(), channel, externalID)
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
	channel, externalID, ok := conversationParams(c)
	if !ok {
		apiError(c, http.StatusBadRequest, "invalid_request", "channel 和 external_id 必填")
		return
	}
	if err := h.Agent.DeleteMessages(c.Request.Context(), channel, externalID); err != nil {
		apiAgentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func conversationParams(c *gin.Context) (string, string, bool) {
	channel := c.Param("channel")
	externalID := c.Param("external_id")
	if channel == "" || externalID == "" || !allowedAPIChannel(channel) {
		return "", "", false
	}
	return channel, externalID, true
}

func allowedAPIChannel(channel string) bool {
	return conversation.IsSupportedChannel(channel)
}

func (h *Handlers) healthz(c *gin.Context) {
	if err := h.Agent.Check(c.Request.Context()); err != nil {
		apiError(c, http.StatusServiceUnavailable, "agent_unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
