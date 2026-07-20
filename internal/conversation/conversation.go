package conversation

import (
	"context"
	"errors"
	"time"
)

var (
	ErrConversationBusy = errors.New("conversation busy")
	ErrLeaseLost        = errors.New("conversation generation lease lost")
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	MaxExternalIDLength   = 128
	DefaultConversationID = "direct-haruhi"
)

type Identity struct {
	Channel    string
	ExternalID string
}

type Turn struct {
	TurnID         string
	Role           string
	Content        string
	CreatedAt      time.Time
	ConversationID string
	SpeakerKind    string
	SpeakerID      string
	BatchID        string
	Sequence       uint64
	DisplayName    string
	AvatarURL      string
}

type Scope struct {
	Identity       Identity
	ConversationID string
}

const (
	SpeakerUser      = "user"
	SpeakerCharacter = "character"

	BatchGenerating = "generating"
	BatchComplete   = "complete"
	BatchPartial    = "partial"
	BatchFailed     = "failed"
)

type Batch struct {
	BatchID           string
	ClientRequestID   string
	ConversationID    string
	PlannedSpeakerIDs []string
	UserMessage       Turn
	CharacterMessages []Turn
	Status            string
	InterruptionCode  string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type BeginState string

const (
	BeginStarted  BeginState = "started"
	BeginExisting BeginState = "existing"
)

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
