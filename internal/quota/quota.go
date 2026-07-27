package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrExhausted          = errors.New("daily quota exhausted")
	ErrInvalidReservation = errors.New("invalid quota reservation")
)

type Subject struct {
	UserID  string
	IsAdmin bool
}

type Outcome string

const (
	OutcomePending   Outcome = "pending"
	OutcomeDelivered Outcome = "delivered"
	OutcomeFailed    Outcome = "failed"
)

type Snapshot struct {
	Unlimited bool
	Limit     int
	Used      int
	Remaining int
	ResetAt   time.Time
	Revision  int64
}

func (s Snapshot) MarshalJSON() ([]byte, error) {
	if s.Unlimited {
		return []byte(`{"unlimited":true}`), nil
	}
	return json.Marshal(struct {
		Unlimited bool   `json:"unlimited"`
		Limit     int    `json:"limit"`
		Used      int    `json:"used"`
		Remaining int    `json:"remaining"`
		ResetAt   string `json:"reset_at"`
		Revision  int64  `json:"revision"`
	}{
		Unlimited: false,
		Limit:     s.Limit,
		Used:      s.Used,
		Remaining: s.Remaining,
		ResetAt:   s.ResetAt.Format(time.RFC3339),
		Revision:  s.Revision,
	})
}

type Reservation struct {
	subject     Subject
	key         string
	requestHash string
	expiresAt   time.Time
	unlimited   bool
}

type Manager interface {
	Reserve(context.Context, Subject, string, time.Time) (Reservation, Snapshot, error)
	Settle(context.Context, Reservation, Outcome, time.Time) (Snapshot, error)
	Snapshot(context.Context, Subject, time.Time) (Snapshot, error)
}

type Redis struct {
	client *redis.Client
	prefix string
	limit  int
}

var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

var reserveScript = redis.NewScript(`
local state = redis.call('HGET', KEYS[1], ARGV[1])
local committed = tonumber(redis.call('HGET', KEYS[1], 'committed') or '0')
local reserved = tonumber(redis.call('HGET', KEYS[1], 'reserved') or '0')
local revision = tonumber(redis.call('HGET', KEYS[1], 'revision') or '0')
if state then
  redis.call('EXPIREAT', KEYS[1], ARGV[3])
  return {1, committed + reserved, 'current', revision}
end
local previousState = redis.call('HGET', KEYS[2], ARGV[1])
if previousState then
  redis.call('EXPIREAT', KEYS[2], ARGV[4])
  return {1, committed + reserved, 'previous', revision}
end
if committed + reserved >= tonumber(ARGV[2]) then
  return {0, committed + reserved, 'exhausted', revision}
end
redis.call('HSET', KEYS[1], ARGV[1], 'reserved')
reserved = redis.call('HINCRBY', KEYS[1], 'reserved', 1)
revision = redis.call('HINCRBY', KEYS[1], 'revision', 1)
redis.call('EXPIREAT', KEYS[1], ARGV[3])
return {1, committed + reserved, 'current', revision}
`)

var settleScript = redis.NewScript(`
local state = redis.call('HGET', KEYS[1], ARGV[1])
local committed = tonumber(redis.call('HGET', KEYS[1], 'committed') or '0')
local reserved = tonumber(redis.call('HGET', KEYS[1], 'reserved') or '0')
local revision = tonumber(redis.call('HGET', KEYS[1], 'revision') or '0')
if state == 'reserved' and ARGV[2] == 'delivered' then
  reserved = redis.call('HINCRBY', KEYS[1], 'reserved', -1)
  committed = redis.call('HINCRBY', KEYS[1], 'committed', 1)
  redis.call('HSET', KEYS[1], ARGV[1], 'delivered')
  revision = redis.call('HINCRBY', KEYS[1], 'revision', 1)
elseif state == 'reserved' and ARGV[2] == 'failed' then
  reserved = redis.call('HINCRBY', KEYS[1], 'reserved', -1)
  redis.call('HDEL', KEYS[1], ARGV[1])
  revision = redis.call('HINCRBY', KEYS[1], 'revision', 1)
end
if redis.call('EXISTS', KEYS[1]) == 1 then
  redis.call('EXPIREAT', KEYS[1], ARGV[3])
end
if reserved < 0 then reserved = 0 end
return {committed + reserved, revision}
`)

var snapshotScript = redis.NewScript(`
local committed = tonumber(redis.call('HGET', KEYS[1], 'committed') or '0')
local reserved = tonumber(redis.call('HGET', KEYS[1], 'reserved') or '0')
local revision = tonumber(redis.call('HGET', KEYS[1], 'revision') or '0')
return {committed + reserved, revision}
`)

func NewRedis(client *redis.Client, prefix string, limit int) (*Redis, error) {
	if client == nil {
		return nil, errors.New("quota redis client is required")
	}
	if strings.TrimSpace(prefix) == "" {
		return nil, errors.New("quota key prefix is required")
	}
	if limit <= 0 {
		return nil, errors.New("quota limit must be positive")
	}
	return &Redis{client: client, prefix: prefix, limit: limit}, nil
}

func (r *Redis) Reserve(ctx context.Context, subject Subject, requestID string, now time.Time) (Reservation, Snapshot, error) {
	if subject.IsAdmin {
		return Reservation{subject: subject, unlimited: true}, Snapshot{Unlimited: true}, nil
	}
	if strings.TrimSpace(subject.UserID) == "" || strings.TrimSpace(requestID) == "" {
		return Reservation{}, Snapshot{}, ErrInvalidReservation
	}
	key, resetAt, expiresAt := r.window(subject.UserID, now)
	previousKey, _, previousExpiresAt := r.window(subject.UserID, now.In(beijing).AddDate(0, 0, -1))
	hash := sha256.Sum256([]byte(requestID))
	requestHash := "request:" + hex.EncodeToString(hash[:])
	result, err := reserveScript.Run(
		ctx, r.client, []string{key, previousKey}, requestHash, r.limit, expiresAt.Unix(), previousExpiresAt.Unix(),
	).Slice()
	if err != nil {
		return Reservation{}, Snapshot{}, fmt.Errorf("reserve daily quota: %w", err)
	}
	if len(result) < 4 {
		return Reservation{}, Snapshot{}, errors.New("reserve daily quota returned an invalid result")
	}
	accepted, err := resultInt(result[0])
	if err != nil {
		return Reservation{}, Snapshot{}, err
	}
	used, err := resultInt(result[1])
	if err != nil {
		return Reservation{}, Snapshot{}, err
	}
	revision, err := resultInt64(result[3])
	if err != nil {
		return Reservation{}, Snapshot{}, err
	}
	snapshot := r.limitedSnapshot(used, revision, resetAt)
	if accepted == 0 {
		return Reservation{}, snapshot, ErrExhausted
	}
	if len(result) < 3 {
		return Reservation{}, Snapshot{}, errors.New("reserve daily quota returned no ledger source")
	}
	reservationKey, reservationExpiry := key, expiresAt
	if source, _ := result[2].(string); source == "previous" {
		reservationKey = previousKey
		reservationExpiry = previousExpiresAt
	}
	return Reservation{
		subject: subject, key: reservationKey, requestHash: requestHash, expiresAt: reservationExpiry,
	}, snapshot, nil
}

func (r *Redis) Settle(ctx context.Context, reservation Reservation, outcome Outcome, now time.Time) (Snapshot, error) {
	if reservation.unlimited || reservation.subject.IsAdmin {
		return Snapshot{Unlimited: true}, nil
	}
	if reservation.key == "" || reservation.requestHash == "" || reservation.subject.UserID == "" {
		return Snapshot{}, ErrInvalidReservation
	}
	if outcome != OutcomePending && outcome != OutcomeDelivered && outcome != OutcomeFailed {
		return Snapshot{}, fmt.Errorf("%w: unknown outcome %q", ErrInvalidReservation, outcome)
	}
	result, err := settleScript.Run(
		ctx, r.client, []string{reservation.key}, reservation.requestHash, string(outcome), reservation.expiresAt.Unix(),
	).Slice()
	if err != nil {
		return Snapshot{}, fmt.Errorf("settle daily quota: %w", err)
	}
	if len(result) < 2 {
		return Snapshot{}, errors.New("settle daily quota returned an invalid result")
	}
	used, err := resultInt(result[0])
	if err != nil {
		return Snapshot{}, err
	}
	revision, err := resultInt64(result[1])
	if err != nil {
		return Snapshot{}, err
	}
	currentKey, resetAt, _ := r.window(reservation.subject.UserID, now)
	if currentKey != reservation.key {
		return r.Snapshot(ctx, reservation.subject, now)
	}
	return r.limitedSnapshot(used, revision, resetAt), nil
}

func (r *Redis) Snapshot(ctx context.Context, subject Subject, now time.Time) (Snapshot, error) {
	if subject.IsAdmin {
		return Snapshot{Unlimited: true}, nil
	}
	if strings.TrimSpace(subject.UserID) == "" {
		return Snapshot{}, ErrInvalidReservation
	}
	key, resetAt, _ := r.window(subject.UserID, now)
	result, err := snapshotScript.Run(ctx, r.client, []string{key}).Slice()
	if err != nil {
		return Snapshot{}, fmt.Errorf("read daily quota: %w", err)
	}
	if len(result) < 2 {
		return Snapshot{}, errors.New("read daily quota returned an invalid result")
	}
	used, err := resultInt(result[0])
	if err != nil {
		return Snapshot{}, err
	}
	revision, err := resultInt64(result[1])
	if err != nil {
		return Snapshot{}, err
	}
	return r.limitedSnapshot(used, revision, resetAt), nil
}

func (r *Redis) window(userID string, now time.Time) (string, time.Time, time.Time) {
	local := now.In(beijing)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, beijing)
	resetAt := start.AddDate(0, 0, 1)
	expiresAt := start.AddDate(0, 0, 2)
	return r.prefix + userID + ":" + start.Format("2006-01-02"), resetAt, expiresAt
}

func (r *Redis) limitedSnapshot(used int, revision int64, resetAt time.Time) Snapshot {
	if used < 0 {
		used = 0
	}
	remaining := r.limit - used
	if remaining < 0 {
		remaining = 0
	}
	return Snapshot{Limit: r.limit, Used: used, Remaining: remaining, ResetAt: resetAt, Revision: revision}
}

func resultInt(value any) (int, error) {
	switch typed := value.(type) {
	case int64:
		return int(typed), nil
	case string:
		var parsed int
		if _, err := fmt.Sscan(typed, &parsed); err != nil {
			return 0, fmt.Errorf("invalid quota integer %q: %w", typed, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("invalid quota integer result %T", value)
	}
}

func resultInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		var parsed int64
		if _, err := fmt.Sscan(typed, &parsed); err != nil {
			return 0, fmt.Errorf("invalid quota revision %q: %w", typed, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("invalid quota revision result %T", value)
	}
}
