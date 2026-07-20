package conversation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client   *redis.Client
	prefix   string
	ttl      time.Duration
	leaseTTL time.Duration
	now      func() time.Time
}

func NewRedisStore(client *redis.Client, prefix string, ttl time.Duration) *RedisStore {
	return &RedisStore{
		client:   client,
		prefix:   prefix,
		ttl:      ttl,
		leaseTTL: 45 * time.Second,
		now:      time.Now,
	}
}

func (s *RedisStore) SetClock(now func() time.Time) {
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func (s *RedisStore) AddTurn(ctx context.Context, identity Identity, role, content string) (Turn, error) {
	createdAt := s.now().In(beijingLocation)
	turn := Turn{
		TurnID:    newTurnID(),
		Role:      role,
		Content:   content,
		CreatedAt: createdAt,
	}
	data, err := json.Marshal(turn)
	if err != nil {
		return Turn{}, err
	}
	date := BeijingDate(createdAt)
	turnsKey := s.turnsKey(identity, date)
	activeKey := s.activeKey(date)
	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, turnsKey, data)
	pipe.SAdd(ctx, activeKey, s.activeMember(identity))
	pipe.PExpire(ctx, turnsKey, s.ttl)
	pipe.PExpire(ctx, activeKey, s.ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return Turn{}, err
	}
	return turn, nil
}

func (s *RedisStore) RecentTurns(ctx context.Context, identity Identity, limit int64) ([]Turn, error) {
	return s.turnsForDate(ctx, identity, BeijingDate(s.now()), limit)
}

func (s *RedisStore) TurnsForDate(ctx context.Context, identity Identity, day time.Time) ([]Turn, error) {
	return s.turnsForDate(ctx, identity, BeijingDate(day), 0)
}

func (s *RedisStore) ActiveIdentities(ctx context.Context, day time.Time) ([]Identity, error) {
	members, err := s.client.SMembers(ctx, s.activeKey(BeijingDate(day))).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Identity, 0, len(members))
	for _, member := range members {
		identity, ok := parseActiveMember(member)
		if ok {
			out = append(out, identity)
		}
	}
	return out, nil
}

func (s *RedisStore) ClearToday(ctx context.Context, identity Identity) error {
	return s.ClearDate(ctx, identity, BeijingDate(s.now()))
}

func (s *RedisStore) ClearDate(ctx context.Context, identity Identity, day time.Time) error {
	date := BeijingDate(day)
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, s.turnsKey(identity, date))
	pipe.SRem(ctx, s.activeKey(date), s.activeMember(identity))
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisStore) turnsForDate(ctx context.Context, identity Identity, date time.Time, limit int64) ([]Turn, error) {
	start := int64(0)
	if limit > 0 {
		start = -limit
	}
	values, err := s.client.LRange(ctx, s.turnsKey(identity, date), start, -1).Result()
	if err != nil {
		return nil, err
	}
	turns := make([]Turn, 0, len(values))
	for _, value := range values {
		var turn Turn
		if err := json.Unmarshal([]byte(value), &turn); err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

func (s *RedisStore) turnsKey(identity Identity, date time.Time) string {
	return fmt.Sprintf("%sconversation:%s:%s:%s", s.prefix, identity.Channel, identity.ExternalID, date.Format("2006-01-02"))
}

func (s *RedisStore) activeKey(date time.Time) string {
	return fmt.Sprintf("%sconversation:active:%s", s.prefix, date.Format("2006-01-02"))
}

func (s *RedisStore) activeMember(identity Identity) string {
	return identity.Channel + "|" + identity.ExternalID
}

func parseActiveMember(member string) (Identity, bool) {
	parts := strings.SplitN(member, "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Identity{}, false
	}
	return Identity{Channel: parts[0], ExternalID: parts[1]}, true
}

func newTurnID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("turn-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
