package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"companion-ai/internal/wechat"
)

type Handlers struct {
	Token  string
	Agent  AgentCaller
	Tokens TokenSource
	Pusher Pusher
	Log    *slog.Logger
	Dedupe *MsgDeduper

	nowSync *sync.WaitGroup
}

func (h *Handlers) RegisterRoutes(r *gin.Engine) {
	if h.Log == nil {
		h.Log = slog.Default()
	}
	if h.Dedupe == nil {
		h.Dedupe = NewMsgDeduper()
	}
	r.GET("/wechat", h.verify)
	r.POST("/wechat", h.receive)
	h.registerAPI(r)
}

func (h *Handlers) WaitAsync() {
	if h.nowSync != nil {
		h.nowSync.Wait()
	}
}

func (h *Handlers) verify(c *gin.Context) {
	sig := c.Query("signature")
	ts := c.Query("timestamp")
	nonce := c.Query("nonce")
	echo := c.Query("echostr")
	if wechat.CheckSignature(h.Token, ts, nonce, sig) {
		c.String(http.StatusOK, echo)
		return
	}
	c.String(http.StatusForbidden, "invalid signature")
}

func (h *Handlers) receive(c *gin.Context) {
	sig := c.Query("signature")
	ts := c.Query("timestamp")
	nonce := c.Query("nonce")
	if !wechat.CheckSignature(h.Token, ts, nonce, sig) {
		c.String(http.StatusForbidden, "invalid signature")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.String(http.StatusOK, "success")
		return
	}
	msg, err := wechat.ParseIncoming(body)
	if err != nil || msg.MsgType != "text" {
		c.String(http.StatusOK, "success")
		return
	}
	if h.Dedupe.SeenOrAdd(msg.MsgID) {
		c.String(http.StatusOK, "success")
		return
	}

	c.String(http.StatusOK, "success")
	if h.nowSync != nil {
		h.nowSync.Add(1)
	}
	go func(openID, text string) {
		if h.nowSync != nil {
			defer h.nowSync.Done()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.handleAsync(ctx, openID, text)
	}(msg.FromUserName, msg.Content)
}

func (h *Handlers) handleAsync(ctx context.Context, openID, text string) {
	reply, err := h.Agent.Reply(ctx, openID, text)
	if err != nil {
		h.Log.Error("agent reply failed", "open_id", openID, "err", err)
		reply = "哼，本小姐突然走神了，再说一遍！"
	}
	if err := pushTextWithTokenRefresh(ctx, h.Tokens, h.Pusher, openID, reply); err != nil {
		h.Log.Error("push reply failed", "open_id", openID, "err", err)
	}
}
