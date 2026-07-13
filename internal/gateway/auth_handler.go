package gateway

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	authn "companion-ai/internal/auth"
)

const (
	sessionCookie  = "sos_session"
	userContextKey = "auth_user"
)

type credentialsReq struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
}

type verifyReq struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type resetReq struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

func (h *Handlers) registerAuth(r *gin.RouterGroup) {
	r.GET("/captcha", h.authCaptcha)
	r.POST("/register/start", h.authRegisterStart)
	r.POST("/register/resend", h.authRegisterResend)
	r.POST("/register/verify", h.authRegisterVerify)
	r.POST("/login", h.authLogin)
	r.POST("/password/forgot", h.authForgotPassword)
	r.POST("/password/reset", h.authResetPassword)
	r.GET("/session", h.requireUser(), h.authSession)
	r.POST("/logout", h.requireUser(), h.authLogout)
}

func (h *Handlers) authCaptcha(c *gin.Context) {
	id, image, err := h.Auth.NewCaptcha(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusServiceUnavailable, "captcha_unavailable", "验证码暂时不可用")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"captcha_id": id, "image": image})
}

func (h *Handlers) authRegisterStart(c *gin.Context) {
	var req credentialsReq
	if c.ShouldBindJSON(&req) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "请完整填写注册信息")
		return
	}
	err := h.Auth.StartRegistration(c.Request.Context(), req.Email, req.Password, req.CaptchaID, req.CaptchaAnswer)
	if err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "verification_sent"})
}

func (h *Handlers) authRegisterResend(c *gin.Context) {
	var req struct {
		Email string `json:"email"`
	}
	if c.ShouldBindJSON(&req) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "邮箱必填")
		return
	}
	if err := h.Auth.ResendRegistration(c.Request.Context(), req.Email); err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "verification_sent"})
}

func (h *Handlers) authRegisterVerify(c *gin.Context) {
	var req verifyReq
	if c.ShouldBindJSON(&req) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "邮箱和验证码必填")
		return
	}
	user, token, err := h.Auth.VerifyRegistration(c.Request.Context(), req.Email, req.Code)
	if err != nil {
		h.authError(c, err)
		return
	}
	h.setSessionCookie(c, token)
	c.JSON(http.StatusCreated, gin.H{"user": user})
}

func (h *Handlers) authLogin(c *gin.Context) {
	var req credentialsReq
	if c.ShouldBindJSON(&req) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "请完整填写登录信息")
		return
	}
	user, token, err := h.Auth.Login(c.Request.Context(), req.Email, req.Password, req.CaptchaID, req.CaptchaAnswer)
	if err != nil {
		h.authError(c, err)
		return
	}
	h.setSessionCookie(c, token)
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (h *Handlers) authForgotPassword(c *gin.Context) {
	var req credentialsReq
	if c.ShouldBindJSON(&req) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "邮箱和图形验证码必填")
		return
	}
	if err := h.Auth.StartPasswordReset(c.Request.Context(), req.Email, req.CaptchaID, req.CaptchaAnswer); err != nil {
		if errors.Is(err, authn.ErrInvalidCaptcha) {
			h.authError(c, err)
			return
		}
		// Avoid revealing whether an account exists or whether mail delivery failed.
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "if_account_exists_email_sent"})
}

func (h *Handlers) authResetPassword(c *gin.Context) {
	var req resetReq
	if c.ShouldBindJSON(&req) != nil {
		apiError(c, http.StatusBadRequest, "invalid_request", "邮箱、验证码和新密码必填")
		return
	}
	user, token, err := h.Auth.ResetPassword(c.Request.Context(), req.Email, req.Code, req.NewPassword)
	if err != nil {
		h.authError(c, err)
		return
	}
	h.setSessionCookie(c, token)
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (h *Handlers) authSession(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"user": currentUser(c)}) }

func (h *Handlers) authLogout(c *gin.Context) {
	token, _ := c.Cookie(sessionCookie)
	if err := h.Auth.Logout(c.Request.Context(), token); err != nil {
		apiError(c, http.StatusServiceUnavailable, "logout_failed", "暂时无法退出登录")
		return
	}
	h.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

func (h *Handlers) requireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookie)
		if err != nil {
			apiError(c, http.StatusUnauthorized, "unauthorized", "请先登录")
			c.Abort()
			return
		}
		user, err := h.Auth.CurrentUser(c.Request.Context(), token)
		if err != nil {
			h.clearSessionCookie(c)
			apiError(c, http.StatusUnauthorized, "unauthorized", "登录状态已失效")
			c.Abort()
			return
		}
		c.Set(userContextKey, user)
		c.Next()
	}
}

func currentUser(c *gin.Context) authn.User {
	value, _ := c.Get(userContextKey)
	user, _ := value.(authn.User)
	return user
}

func (h *Handlers) setSessionCookie(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int((7 * 24 * time.Hour).Seconds()), HttpOnly: true, Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (h *Handlers) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (h *Handlers) authError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authn.ErrInvalidCredentials):
		apiError(c, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码不正确")
	case errors.Is(err, authn.ErrInvalidCaptcha):
		apiError(c, http.StatusBadRequest, "invalid_captcha", "图形验证码不正确或已过期")
	case errors.Is(err, authn.ErrInvalidCode):
		apiError(c, http.StatusBadRequest, "invalid_code", "邮箱验证码不正确或已过期")
	case errors.Is(err, authn.ErrEmailInUse):
		apiError(c, http.StatusConflict, "email_in_use", "该邮箱已经注册")
	case errors.Is(err, authn.ErrRateLimited):
		apiError(c, http.StatusTooManyRequests, "rate_limited", "请稍后再发送验证码")
	default:
		apiError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}
}
