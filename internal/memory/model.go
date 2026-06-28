package memory

import "time"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type Message struct {
	ID        uint      `gorm:"primaryKey"`
	OpenID    string    `gorm:"index:idx_msg_openid_created;size:64"`
	Role      string    `gorm:"size:16"`
	Content   string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index:idx_msg_openid_created"`
}

type MemorySummary struct {
	ID          uint      `gorm:"primaryKey"`
	OpenID      string    `gorm:"index:idx_sum_openid_date;size:64"`
	SummaryDate time.Time `gorm:"index:idx_sum_openid_date"`
	Content     string    `gorm:"type:text"`
	CreatedAt   time.Time
}
