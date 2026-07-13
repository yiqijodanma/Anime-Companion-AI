package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidCaptcha     = errors.New("invalid or expired captcha")
	ErrInvalidCode        = errors.New("invalid or expired verification code")
	ErrEmailInUse         = errors.New("email already registered")
	ErrRateLimited        = errors.New("please wait before requesting another code")
	ErrUnauthorized       = errors.New("authentication required")
)

type Config struct {
	Pepper       string
	SessionTTL   time.Duration
	CaptchaTTL   time.Duration
	CodeTTL      time.Duration
	ResendWindow time.Duration
}

type User struct {
	ID           string    `gorm:"primaryKey;size:32" json:"id"`
	Email        string    `gorm:"uniqueIndex;size:320" json:"email"`
	PasswordHash string    `json:"-"`
	VerifiedAt   time.Time `json:"verified_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type pendingVerification struct {
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash,omitempty"`
	CodeHash     string `json:"code_hash"`
	Attempts     int    `json:"attempts"`
}

type Service struct {
	db     *gorm.DB
	redis  *redis.Client
	mailer Mailer
	cfg    Config
	now    func() time.Time
}

func NewService(db *gorm.DB, redisClient *redis.Client, mailer Mailer, cfg Config) (*Service, error) {
	if db == nil || redisClient == nil || mailer == nil {
		return nil, errors.New("auth dependencies are required")
	}
	if strings.TrimSpace(cfg.Pepper) == "" {
		return nil, errors.New("auth pepper is required")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 7 * 24 * time.Hour
	}
	if cfg.CaptchaTTL == 0 {
		cfg.CaptchaTTL = 5 * time.Minute
	}
	if cfg.CodeTTL == 0 {
		cfg.CodeTTL = 10 * time.Minute
	}
	if cfg.ResendWindow == 0 {
		cfg.ResendWindow = time.Minute
	}
	return &Service{db: db, redis: redisClient, mailer: mailer, cfg: cfg, now: time.Now}, nil
}

func (s *Service) NewCaptcha(ctx context.Context) (string, string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	answer, err := randomFromAlphabet(4, alphabet)
	if err != nil {
		return "", "", err
	}
	id, err := randomToken(18)
	if err != nil {
		return "", "", err
	}
	if err := s.redis.Set(ctx, "auth:captcha:"+id, s.digest(answer), s.cfg.CaptchaTTL).Err(); err != nil {
		return "", "", err
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="180" height="64" viewBox="0 0 180 64"><rect width="180" height="64" fill="#fffaf0"/><path d="M3 18L177 45M5 51L174 12M12 31L168 27" stroke="#b72b27" opacity=".22"/><text x="90" y="43" text-anchor="middle" fill="#1f1c18" font-family="monospace" font-size="32" font-weight="800" letter-spacing="8">%s</text></svg>`, html.EscapeString(answer))
	return id, "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), nil
}

func (s *Service) StartRegistration(ctx context.Context, email, password, captchaID, captchaAnswer string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if err := s.consumeCaptcha(ctx, captchaID, captchaAnswer); err != nil {
		return err
	}
	var count int64
	if err := s.db.Model(&User{}).Where("email = ?", email).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrEmailInUse
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.createPending(ctx, "register", email, hash)
}

func (s *Service) ResendRegistration(ctx context.Context, email string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	value, err := s.redis.Get(ctx, s.pendingKey("register", email)).Bytes()
	if err != nil {
		return ErrInvalidCode
	}
	var pending pendingVerification
	if json.Unmarshal(value, &pending) != nil {
		return ErrInvalidCode
	}
	return s.createPending(ctx, "register", email, pending.PasswordHash)
}

func (s *Service) VerifyRegistration(ctx context.Context, email, code string) (User, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, "", err
	}
	pending, err := s.verifyPending(ctx, "register", email, code)
	if err != nil {
		return User{}, "", err
	}
	id, err := randomHex(16)
	if err != nil {
		return User{}, "", err
	}
	user := User{ID: id, Email: email, PasswordHash: pending.PasswordHash, VerifiedAt: s.now()}
	if err := s.db.Create(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return User{}, "", ErrEmailInUse
		}
		return User{}, "", err
	}
	_ = s.redis.Del(ctx, s.pendingKey("register", email)).Err()
	token, err := s.createSession(ctx, user.ID)
	return user, token, err
}

func (s *Service) Login(ctx context.Context, email, password, captchaID, captchaAnswer string) (User, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, "", ErrInvalidCredentials
	}
	if err := s.consumeCaptcha(ctx, captchaID, captchaAnswer); err != nil {
		return User{}, "", err
	}
	var user User
	if err := s.db.Where("email = ?", email).First(&user).Error; err != nil || !verifyPassword(user.PasswordHash, password) {
		return User{}, "", ErrInvalidCredentials
	}
	token, err := s.createSession(ctx, user.ID)
	return user, token, err
}

func (s *Service) StartPasswordReset(ctx context.Context, email, captchaID, captchaAnswer string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil
	}
	if err := s.consumeCaptcha(ctx, captchaID, captchaAnswer); err != nil {
		return err
	}
	var user User
	if err := s.db.Where("email = ?", email).First(&user).Error; err != nil {
		return nil
	}
	return s.createPending(ctx, "reset", email, "")
}

func (s *Service) ResetPassword(ctx context.Context, email, code, newPassword string) (User, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, "", ErrInvalidCode
	}
	if _, err := s.verifyPending(ctx, "reset", email, code); err != nil {
		return User{}, "", err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return User{}, "", err
	}
	var user User
	if err := s.db.Where("email = ?", email).First(&user).Error; err != nil {
		return User{}, "", ErrInvalidCode
	}
	if err := s.db.Model(&user).Updates(map[string]any{"password_hash": hash, "updated_at": s.now()}).Error; err != nil {
		return User{}, "", err
	}
	if err := s.RevokeAllSessions(ctx, user.ID); err != nil {
		return User{}, "", err
	}
	_ = s.redis.Del(ctx, s.pendingKey("reset", email)).Err()
	token, err := s.createSession(ctx, user.ID)
	return user, token, err
}

func (s *Service) CurrentUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrUnauthorized
	}
	id, err := s.redis.Get(ctx, s.sessionKey(token)).Result()
	if err != nil {
		return User{}, ErrUnauthorized
	}
	var user User
	if err := s.db.First(&user, "id = ?", id).Error; err != nil {
		return User{}, ErrUnauthorized
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	key := s.sessionKey(token)
	id, _ := s.redis.Get(ctx, key).Result()
	pipe := s.redis.TxPipeline()
	pipe.Del(ctx, key)
	if id != "" {
		pipe.SRem(ctx, "auth:user_sessions:"+id, key)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID string) error {
	setKey := "auth:user_sessions:" + userID
	keys, err := s.redis.SMembers(ctx, setKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	if len(keys) > 0 {
		keys = append(keys, setKey)
		return s.redis.Del(ctx, keys...).Err()
	}
	return s.redis.Del(ctx, setKey).Err()
}

func (s *Service) createPending(ctx context.Context, purpose, email, passwordHash string) error {
	cooldown := "auth:cooldown:" + purpose + ":" + email
	ok, err := s.redis.SetNX(ctx, cooldown, "1", s.cfg.ResendWindow).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrRateLimited
	}
	code, err := randomFromAlphabet(6, "0123456789")
	if err != nil {
		return err
	}
	pending := pendingVerification{Email: email, PasswordHash: passwordHash, CodeHash: s.digest(code)}
	data, _ := json.Marshal(pending)
	if err := s.redis.Set(ctx, s.pendingKey(purpose, email), data, s.cfg.CodeTTL).Err(); err != nil {
		return err
	}
	if err := s.mailer.SendVerification(ctx, email, code, purpose); err != nil {
		_ = s.redis.Del(ctx, s.pendingKey(purpose, email), cooldown).Err()
		return err
	}
	return nil
}

func (s *Service) verifyPending(ctx context.Context, purpose, email, code string) (pendingVerification, error) {
	key := s.pendingKey(purpose, email)
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil {
		return pendingVerification{}, ErrInvalidCode
	}
	var pending pendingVerification
	if json.Unmarshal(data, &pending) != nil || pending.Attempts >= 5 {
		return pendingVerification{}, ErrInvalidCode
	}
	if !hmac.Equal([]byte(pending.CodeHash), []byte(s.digest(strings.TrimSpace(code)))) {
		pending.Attempts++
		updated, _ := json.Marshal(pending)
		ttl := s.redis.TTL(ctx, key).Val()
		if pending.Attempts >= 5 {
			_ = s.redis.Del(ctx, key).Err()
		} else {
			_ = s.redis.Set(ctx, key, updated, ttl).Err()
		}
		return pendingVerification{}, ErrInvalidCode
	}
	return pending, nil
}

func (s *Service) consumeCaptcha(ctx context.Context, id, answer string) error {
	key := "auth:captcha:" + strings.TrimSpace(id)
	want, err := s.redis.GetDel(ctx, key).Result()
	if err != nil || !hmac.Equal([]byte(want), []byte(s.digest(strings.ToUpper(strings.TrimSpace(answer))))) {
		return ErrInvalidCaptcha
	}
	return nil
}

func (s *Service) createSession(ctx context.Context, userID string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	key := s.sessionKey(token)
	setKey := "auth:user_sessions:" + userID
	pipe := s.redis.TxPipeline()
	pipe.Set(ctx, key, userID, s.cfg.SessionTTL)
	pipe.SAdd(ctx, setKey, key)
	pipe.Expire(ctx, setKey, s.cfg.SessionTTL)
	_, err = pipe.Exec(ctx)
	return token, err
}

func (s *Service) sessionKey(token string) string { return "auth:session:" + s.digest(token) }
func (s *Service) pendingKey(purpose, email string) string {
	return "auth:pending:" + purpose + ":" + email
}
func (s *Service) digest(value string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.Pepper))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || strings.ToLower(parsed.Address) != value || len(value) > 320 {
		return "", errors.New("invalid email address")
	}
	return value, nil
}

func randomFromAlphabet(length int, alphabet string) (string, error) {
	buf := make([]byte, length)
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range raw {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf), nil
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
