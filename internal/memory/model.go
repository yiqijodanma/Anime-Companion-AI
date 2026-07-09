package memory

import (
	"strconv"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type Message struct {
	ID          uint      `gorm:"primaryKey"`
	OpenID      string    `gorm:"index:idx_msg_openid_created;size:128"`
	Channel     string    `gorm:"size:16;default:wechat;uniqueIndex:idx_msg_identity_date_turn;index:idx_msg_identity_created;index:idx_msg_date_channel"`
	ExternalID  string    `gorm:"size:128;uniqueIndex:idx_msg_identity_date_turn;index:idx_msg_identity_created"`
	TurnID      string    `gorm:"size:64;uniqueIndex:idx_msg_identity_date_turn"`
	Role        string    `gorm:"size:16"`
	Content     string    `gorm:"type:text"`
	MessageDate time.Time `gorm:"type:date;uniqueIndex:idx_msg_identity_date_turn;index:idx_msg_date_channel"`
	ArchivedAt  time.Time
	CreatedAt   time.Time `gorm:"index:idx_msg_openid_created;index:idx_msg_identity_created"`
}

type MemorySummary struct {
	ID          uint      `gorm:"primaryKey"`
	OpenID      string    `gorm:"index:idx_sum_openid_date;size:128"`
	Channel     string    `gorm:"size:16;default:wechat;uniqueIndex:idx_sum_identity_date_unique;index:idx_sum_identity_date"`
	ExternalID  string    `gorm:"size:128;uniqueIndex:idx_sum_identity_date_unique;index:idx_sum_identity_date"`
	SummaryDate time.Time `gorm:"index:idx_sum_openid_date"`
	MessageDate time.Time `gorm:"type:date;uniqueIndex:idx_sum_identity_date_unique;index:idx_sum_identity_date"`
	Content     string    `gorm:"type:text"`
	ArchivedAt  time.Time
	CreatedAt   time.Time
}

var legacyTurnCounter uint64

func (m *Message) BeforeCreate(_ *gorm.DB) error {
	now := time.Now()
	if m.Channel == "" {
		m.Channel = "wechat"
	}
	if m.ExternalID == "" {
		m.ExternalID = m.OpenID
	}
	if m.OpenID == "" {
		m.OpenID = m.ExternalID
	}
	if m.TurnID == "" {
		m.TurnID = "legacy-" + strconv.FormatInt(now.UnixNano(), 10) + "-" + strconv.FormatUint(atomic.AddUint64(&legacyTurnCounter, 1), 10)
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.MessageDate.IsZero() {
		m.MessageDate = beijingDate(m.CreatedAt)
	}
	if m.ArchivedAt.IsZero() {
		m.ArchivedAt = now
	}
	return nil
}

func (s *MemorySummary) BeforeCreate(_ *gorm.DB) error {
	now := time.Now()
	if s.Channel == "" {
		s.Channel = "wechat"
	}
	if s.ExternalID == "" {
		s.ExternalID = s.OpenID
	}
	if s.OpenID == "" {
		s.OpenID = s.ExternalID
	}
	if s.SummaryDate.IsZero() {
		s.SummaryDate = now
	}
	if s.MessageDate.IsZero() {
		s.MessageDate = beijingDate(s.SummaryDate)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.ArchivedAt.IsZero() {
		s.ArchivedAt = now
	}
	return nil
}
