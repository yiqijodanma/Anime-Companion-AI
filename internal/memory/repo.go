package memory

import (
	"time"

	"gorm.io/gorm"
)

type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) (*Repo, error) {
	if err := db.AutoMigrate(&Message{}, &MemorySummary{}); err != nil {
		return nil, err
	}
	return &Repo{db: db}, nil
}

func (r *Repo) DB() *gorm.DB {
	return r.db
}

func startOfToday() time.Time {
	now := time.Now()
	start, _ := dayRange(now)
	return start
}

func dayRange(day time.Time) (time.Time, time.Time) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return start, start.AddDate(0, 0, 1)
}

func (r *Repo) SaveMessage(openID, role, content string) error {
	return r.db.Create(&Message{
		OpenID:    openID,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}).Error
}

func (r *Repo) TodayMessages(openID string) ([]Message, error) {
	return r.MessagesForDate(openID, time.Now())
}

func (r *Repo) MessagesForDate(openID string, day time.Time) ([]Message, error) {
	start, end := dayRange(day)
	var msgs []Message
	err := r.db.Where("open_id = ? AND created_at >= ? AND created_at < ?", openID, start, end).
		Order("created_at asc").Find(&msgs).Error
	return msgs, err
}

func (r *Repo) RecentSummaries(openID string) ([]MemorySummary, error) {
	cutoff := startOfToday().AddDate(0, 0, -7)
	var sums []MemorySummary
	err := r.db.Where("open_id = ? AND summary_date >= ?", openID, cutoff).
		Order("summary_date asc").Find(&sums).Error
	return sums, err
}

func (r *Repo) SaveSummary(openID string, date time.Time, content string) error {
	return r.db.Create(&MemorySummary{
		OpenID:      openID,
		SummaryDate: date,
		Content:     content,
		CreatedAt:   time.Now(),
	}).Error
}

func (r *Repo) ActiveOpenIDsForDate(day time.Time) ([]string, error) {
	start, end := dayRange(day)
	var ids []string
	err := r.db.Model(&Message{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Distinct().Pluck("open_id", &ids).Error
	return ids, err
}

func (r *Repo) DeleteTodayMessages(openID string) error {
	return r.DeleteMessagesForDate(openID, time.Now())
}

func (r *Repo) DeleteMessagesForDate(openID string, day time.Time) error {
	start, end := dayRange(day)
	return r.db.Where("open_id = ? AND created_at >= ? AND created_at < ?", openID, start, end).
		Delete(&Message{}).Error
}

func (r *Repo) PurgeSummariesOlderThan(cutoff time.Time) error {
	return r.db.Where("summary_date < ?", cutoff).Delete(&MemorySummary{}).Error
}
