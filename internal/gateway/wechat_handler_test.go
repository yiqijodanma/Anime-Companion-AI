package gateway

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func sign(token, ts, nonce string) string {
	parts := []string{token, ts, nonce}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func newTestHandlers(agent *fakeAgent, push *fakePusher) *Handlers {
	return &Handlers{
		Token:   "mytoken",
		Agent:   agent,
		Tokens:  &fakeTokens{token: "TOK"},
		Pusher:  push,
		Log:     slogDiscard(),
		Dedupe:  NewMsgDeduper(),
		nowSync: &sync.WaitGroup{},
	}
}

func TestWechatGetVerify(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newTestHandlers(&fakeAgent{}, &fakePusher{})
	h.RegisterRoutes(r)

	ts, nonce, echo := "1717000000", "abc", "hello_echo"
	req := httptest.NewRequest(http.MethodGet,
		"/wechat?signature="+sign("mytoken", ts, nonce)+"&timestamp="+ts+"&nonce="+nonce+"&echostr="+echo, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, echo, w.Body.String())
}

func TestWechatPostAcksAndPushes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{reply: "哼，收到！"}
	push := &fakePusher{}
	h := newTestHandlers(agent, push)
	h.RegisterRoutes(r)

	ts, nonce := "1717000000", "abc"
	body := `<xml><FromUserName><![CDATA[u1]]></FromUserName><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[在吗]]></Content><MsgId>1</MsgId></xml>`
	req := httptest.NewRequest(http.MethodPost,
		"/wechat?signature="+sign("mytoken", ts, nonce)+"&timestamp="+ts+"&nonce="+nonce, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())
	h.WaitAsync()
	require.Equal(t, 1, agent.Calls())
	require.Equal(t, "TOK:哼，收到！", push.sent["u1"])
}

func TestWechatPostDedupesMsgID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{reply: "哼，收到！"}
	h := newTestHandlers(agent, &fakePusher{})
	h.RegisterRoutes(r)

	ts, nonce := "1717000000", "abc"
	url := "/wechat?signature=" + sign("mytoken", ts, nonce) + "&timestamp=" + ts + "&nonce=" + nonce
	body := `<xml><FromUserName><![CDATA[u1]]></FromUserName><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[在吗]]></Content><MsgId>dup</MsgId></xml>`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(body)))
		require.Equal(t, "success", w.Body.String())
	}

	h.WaitAsync()
	require.Equal(t, 1, agent.Calls())
}

func TestWechatPostDedupeErrorFailOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{reply: "哼，收到！"}
	push := &fakePusher{}
	h := newTestHandlers(agent, push)
	h.Dedupe = &fakeDeduper{err: errors.New("redis down")}
	h.RegisterRoutes(r)

	ts, nonce := "1717000000", "abc"
	url := "/wechat?signature=" + sign("mytoken", ts, nonce) + "&timestamp=" + ts + "&nonce=" + nonce
	body := `<xml><FromUserName><![CDATA[u1]]></FromUserName><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[在吗]]></Content><MsgId>dedupe-error</MsgId></xml>`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(body)))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())
	h.WaitAsync()
	require.Equal(t, 1, agent.Calls())
	require.Equal(t, "TOK:哼，收到！", push.sent["u1"])
}

func TestWechatPostRateLimitedAcksWithoutAgentCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{reply: "should not be used"}
	push := &fakePusher{}
	h := newTestHandlers(agent, push)
	limiter := &fakeLimiter{allow: false}
	h.Limiter = limiter
	h.RegisterRoutes(r)

	ts, nonce := "1717000000", "abc"
	url := "/wechat?signature=" + sign("mytoken", ts, nonce) + "&timestamp=" + ts + "&nonce=" + nonce
	body := `<xml><FromUserName><![CDATA[u1]]></FromUserName><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[在吗]]></Content><MsgId>limited</MsgId></xml>`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(body)))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())
	h.WaitAsync()
	require.Equal(t, 0, agent.Calls())
	require.Empty(t, push.sent)
	require.Equal(t, 1, limiter.calls)
	require.Equal(t, "u1", limiter.lastOpenID)
}

func TestWechatPostLimiterErrorFailOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{reply: "哼，收到！"}
	push := &fakePusher{}
	h := newTestHandlers(agent, push)
	h.Limiter = &fakeLimiter{err: errors.New("redis down")}
	h.RegisterRoutes(r)

	ts, nonce := "1717000000", "abc"
	url := "/wechat?signature=" + sign("mytoken", ts, nonce) + "&timestamp=" + ts + "&nonce=" + nonce
	body := `<xml><FromUserName><![CDATA[u1]]></FromUserName><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[在吗]]></Content><MsgId>limiter-error</MsgId></xml>`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(body)))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())
	h.WaitAsync()
	require.Equal(t, 1, agent.Calls())
	require.Equal(t, "TOK:哼，收到！", push.sent["u1"])
}
