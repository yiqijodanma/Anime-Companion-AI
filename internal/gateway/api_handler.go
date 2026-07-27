package gateway

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	if h.Auth == nil && h.AuthenticateSession == nil {
		// Legacy mode is retained for isolated handler tests and non-web deployments.
		v1.POST("/chat", h.apiChat)
		v1.GET("/conversations/:channel/:external_id/messages", h.apiListMessages)
		v1.DELETE("/conversations/:channel/:external_id/messages", h.apiDeleteMessages)
	} else {
		if h.Auth != nil {
			h.registerAuth(v1.Group("/auth"))
		}
		web := v1.Group("")
		web.Use(h.requireUser())
		web.GET("/conversations", h.webListConversationSpaces)
		web.GET("/conversations/:conversation_id/messages", h.webListConversationMessages)
		web.POST("/conversations/:conversation_id/messages", h.webSendConversationMessage)
		web.DELETE("/conversations/:conversation_id/messages", h.webDeleteConversationMessages)
		web.GET("/conversations/messages", h.webListMessages)
		web.POST("/conversations/messages", h.webSendMessage)
		web.DELETE("/conversations/messages", h.webDeleteMessages)
	}
	r.GET("/healthz", h.healthz)
}

func (h *Handlers) webListConversationSpaces(c *gin.Context) {
	user := currentUser(c)
	spaces, err := h.Agent.ListConversationSpaces(c.Request.Context(), "api", user.ID)
	if err != nil {
		apiAgentError(c, err)
		return
	}
	if spaces == nil {
		spaces = []ConversationSpace{}
	}
	c.JSON(http.StatusOK, gin.H{"conversations": spaces})
}

type webMessageReq struct {
	Content         string `json:"content"`
	ClientRequestID string `json:"client_request_id"`
}

func (h *Handlers) webListConversationMessages(c *gin.Context) {
	user := currentUser(c)
	messages, err := h.Agent.ListConversationMessages(c.Request.Context(), "api", user.ID, c.Param("conversation_id"))
	if err != nil {
		apiAgentError(c, err)
		return
	}
	if messages == nil {
		messages = []ConversationMessage{}
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handlers) webSendConversationMessage(c *gin.Context) {
	var req webMessageReq
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Content) == "" || strings.TrimSpace(req.ClientRequestID) == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "content 和 client_request_id 必填")
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Content)) > 4000 {
		apiError(c, http.StatusRequestEntityTooLarge, "message_too_large", "消息不能超过 4000 个字符")
		return
	}
	requestID := strings.TrimSpace(req.ClientRequestID)
	if uuid.Validate(requestID) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "client_request_id 格式不正确")
		return
	}
	user := currentUser(c)
	if !h.allowOpenID(c.Request.Context(), user.ID, "web_chat") {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	conversationID := c.Param("conversation_id")
	reservation, _, ok := h.reserveQuota(c, user, scopedQuotaRequestID(conversationID, requestID))
	if !ok {
		return
	}
	batch, err := h.Agent.SendConversationMessage(
		c.Request.Context(), "api", user.ID, conversationID, req.Content, requestID,
	)
	if err != nil {
		if !h.settleAgentError(c, reservation, err) {
			return
		}
		apiAgentError(c, err)
		return
	}
	snapshot, err := h.settleQuota(c.Request.Context(), reservation, quotaOutcome(batch))
	if err != nil {
		quotaUnavailable(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"batch": batch, "quota": snapshot})
}

func (h *Handlers) webDeleteConversationMessages(c *gin.Context) {
	user := currentUser(c)
	if err := h.Agent.DeleteConversationMessages(c.Request.Context(), "api", user.ID, c.Param("conversation_id")); err != nil {
		apiAgentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handlers) webListMessages(c *gin.Context) {
	markDeprecatedConversationAlias(c)
	user := currentUser(c)
	messages, err := h.Agent.ListConversationMessages(c.Request.Context(), "api", user.ID, conversation.DefaultConversationID)
	if err != nil {
		apiAgentError(c, err)
		return
	}
	if messages == nil {
		messages = []ConversationMessage{}
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handlers) webSendMessage(c *gin.Context) {
	markDeprecatedConversationAlias(c)
	var req webMessageReq
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Content) == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "content 必填")
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Content)) > 4000 {
		apiError(c, http.StatusRequestEntityTooLarge, "message_too_large", "消息不能超过 4000 个字符")
		return
	}
	requestID := strings.TrimSpace(req.ClientRequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	} else if uuid.Validate(requestID) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "client_request_id 格式不正确")
		return
	}
	user := currentUser(c)
	if !h.allowOpenID(c.Request.Context(), user.ID, "web_chat") {
		apiError(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	reservation, _, ok := h.reserveQuota(c, user, scopedQuotaRequestID(conversation.DefaultConversationID, requestID))
	if !ok {
		return
	}
	batch, err := h.Agent.SendConversationMessage(c.Request.Context(), "api", user.ID, conversation.DefaultConversationID, req.Content, requestID)
	if err != nil {
		if !h.settleAgentError(c, reservation, err) {
			return
		}
		apiAgentError(c, err)
		return
	}
	snapshot, err := h.settleQuota(c.Request.Context(), reservation, quotaOutcome(batch))
	if err != nil {
		quotaUnavailable(c)
		return
	}
	if len(batch.CharacterMessages) == 0 {
		apiError(c, http.StatusBadGateway, "agent_error", "agent unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": batch.CharacterMessages[0].Content, "quota": snapshot})
}

func (h *Handlers) webDeleteMessages(c *gin.Context) {
	markDeprecatedConversationAlias(c)
	user := currentUser(c)
	if err := h.Agent.DeleteConversationMessages(c.Request.Context(), "api", user.ID, conversation.DefaultConversationID); err != nil {
		apiAgentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func markDeprecatedConversationAlias(c *gin.Context) {
	c.Header("Deprecation", "true")
	c.Header("Link", `</api/v1/conversations/direct-haruhi/messages>; rel="successor-version"`)
}

func apiError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

func apiAgentError(c *gin.Context, err error) {
	switch grpcstatus.Code(err) {
	case codes.InvalidArgument:
		apiError(c, http.StatusBadRequest, "invalid_request", "invalid request")
	case codes.ResourceExhausted:
		apiError(c, http.StatusRequestEntityTooLarge, "message_too_large", "message is too large")
	case codes.NotFound:
		apiError(c, http.StatusNotFound, "conversation_not_found", "conversation not found")
	case codes.Aborted:
		apiError(c, http.StatusConflict, "conversation_busy", "conversation is busy")
	default:
		apiError(c, http.StatusBadGateway, "agent_error", "agent unavailable")
	}
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
		apiAgentError(c, err)
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
