package conversation

import (
	"context"
	"time"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	MaxExternalIDLength = 128
)

type Identity struct {
	Channel    string
	ExternalID string
}

type Turn struct {
	TurnID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

type Store interface {
	AddTurn(ctx context.Context, identity Identity, role, content string) (Turn, error)
	RecentTurns(ctx context.Context, identity Identity, limit int64) ([]Turn, error)
	TurnsForDate(ctx context.Context, identity Identity, day time.Time) ([]Turn, error)
	ActiveIdentities(ctx context.Context, day time.Time) ([]Identity, error)
	ClearToday(ctx context.Context, identity Identity) error
	ClearDate(ctx context.Context, identity Identity, day time.Time) error
}

var beijingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func BeijingDate(day time.Time) time.Time {
	inBeijing := day.In(beijingLocation)
	return time.Date(inBeijing.Year(), inBeijing.Month(), inBeijing.Day(), 0, 0, 0, 0, beijingLocation)
}

func ParseBeijingDate(value string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", value, beijingLocation)
}

func IsSupportedChannel(channel string) bool {
	switch channel {
	case "api", "wechat":
		return true
	default:
		return false
	}
}
