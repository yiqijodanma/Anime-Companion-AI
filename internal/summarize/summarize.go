package summarize

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"companion-ai/internal/chat"
)

type Summarizer struct {
	model chat.Model
}

func NewSummarizer(m chat.Model) *Summarizer {
	return &Summarizer{model: m}
}

func (s *Summarizer) Summarize(ctx context.Context, history []chat.Turn) (string, error) {
	if len(history) == 0 {
		return "", nil
	}

	var b strings.Builder
	for _, turn := range history {
		b.WriteString(turn.Role)
		b.WriteString(": ")
		b.WriteString(turn.Content)
		b.WriteByte('\n')
	}

	msgs := []*schema.Message{
		schema.SystemMessage("请把以下当天对话整理成一段简短记忆摘要，只保留用户的重要事实、偏好和当天主要事件。"),
		schema.UserMessage(b.String()),
	}
	out, err := s.model.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Content), nil
}
