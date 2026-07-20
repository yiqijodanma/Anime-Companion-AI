package memory

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repo struct {
	db *gorm.DB
}

type ArchiveTurn struct {
	TurnID      string
	Role        string
	Content     string
	CreatedAt   time.Time
	SpeakerKind string
	SpeakerID   string
	BatchID     string
	Sequence    uint64
}

func NewRepo(db *gorm.DB) (*Repo, error) {
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

var beijingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func beijingDate(day time.Time) time.Time {
	inBeijing := day.In(beijingLocation)
	return time.Date(inBeijing.Year(), inBeijing.Month(), inBeijing.Day(), 0, 0, 0, 0, beijingLocation)
}

func (r *Repo) SaveMessage(openID, role, content string) error {
	now := time.Now()
	return r.db.Create(&Message{
		OpenID:      openID,
		Channel:     "wechat",
		ExternalID:  openID,
		Role:        role,
		Content:     content,
		MessageDate: beijingDate(now),
		ArchivedAt:  now,
		CreatedAt:   now,
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
	now := time.Now()
	return r.db.Create(&MemorySummary{
		OpenID:      openID,
		Channel:     "wechat",
		ExternalID:  openID,
		SummaryDate: date,
		MessageDate: beijingDate(date),
		Content:     content,
		ArchivedAt:  now,
		CreatedAt:   now,
	}).Error
}

func (r *Repo) RecentSummariesForIdentity(channel, externalID string) ([]MemorySummary, error) {
	return r.RecentSummariesForScope(channel, externalID, "direct-haruhi")
}

func (r *Repo) RecentSummariesForScope(channel, externalID, conversationID string) ([]MemorySummary, error) {
	cutoff := beijingDate(time.Now()).AddDate(0, 0, -7)
	var sums []MemorySummary
	err := r.db.Where("channel = ? AND external_id = ? AND conversation_id = ? AND message_date >= ?", channel, externalID, conversationID, cutoff).
		Order("message_date asc").Find(&sums).Error
	return sums, err
}

func (r *Repo) ArchiveDailyConversation(channel, externalID string, date time.Time, turns []ArchiveTurn, summary string) error {
	return r.ArchiveDailyConversationForScope(channel, externalID, "direct-haruhi", date, turns, summary)
}

func (r *Repo) ArchiveDailyConversationForScope(channel, externalID, conversationID string, date time.Time, turns []ArchiveTurn, summary string) error {
	messageDate := beijingDate(date)
	now := time.Now()
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, turn := range turns {
			createdAt := turn.CreatedAt
			if createdAt.IsZero() {
				createdAt = now
			}
			msg := Message{
				OpenID:         externalID,
				Channel:        channel,
				ExternalID:     externalID,
				TurnID:         turn.TurnID,
				ConversationID: conversationID,
				SpeakerKind:    turn.SpeakerKind,
				SpeakerID:      turn.SpeakerID,
				BatchID:        turn.BatchID,
				Sequence:       turn.Sequence,
				Role:           turn.Role,
				Content:        turn.Content,
				MessageDate:    messageDate,
				ArchivedAt:     now,
				CreatedAt:      createdAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "channel"}, {Name: "external_id"}, {Name: "conversation_id"}, {Name: "message_date"}, {Name: "turn_id"}},
				DoNothing: true,
			}).Create(&msg).Error; err != nil {
				return err
			}
		}
		if summary == "" {
			return nil
		}
		sum := MemorySummary{
			OpenID:         externalID,
			Channel:        channel,
			ExternalID:     externalID,
			ConversationID: conversationID,
			SummaryDate:    messageDate,
			MessageDate:    messageDate,
			Content:        summary,
			ArchivedAt:     now,
			CreatedAt:      now,
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "channel"}, {Name: "external_id"}, {Name: "conversation_id"}, {Name: "message_date"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"open_id":      externalID,
				"summary_date": messageDate,
				"content":      summary,
				"archived_at":  now,
			}),
		}).Create(&sum).Error
	})
}

func (r *Repo) MessagesForScopeDate(channel, externalID, conversationID string, day time.Time) ([]Message, error) {
	var messages []Message
	err := r.db.Where("channel = ? AND external_id = ? AND conversation_id = ? AND message_date = ?", channel, externalID, conversationID, beijingDate(day)).
		Order("sequence asc, created_at asc, id asc").Find(&messages).Error
	return messages, err
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
