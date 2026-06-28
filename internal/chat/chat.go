package chat

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"companion-ai/internal/persona"
)

type Turn struct {
	Role    string
	Content string
}

type Model interface {
	Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error)
}

type Replier struct {
	model Model
}

func NewReplier(m Model) *Replier {
	return &Replier{model: m}
}

func (r *Replier) Reply(ctx context.Context, summaries []string, history []Turn, userText string) (string, error) {
	msgs := []*schema.Message{schema.SystemMessage(persona.SystemPrompt)}
	if len(summaries) > 0 {
		msgs = append(msgs, schema.SystemMessage("最近 7 天记忆摘要：\n"+strings.Join(summaries, "\n")))
	}
	for _, turn := range history {
		switch turn.Role {
		case "assistant":
			msgs = append(msgs, schema.AssistantMessage(turn.Content, nil))
		default:
			msgs = append(msgs, schema.UserMessage(turn.Content))
		}
	}
	msgs = append(msgs, schema.UserMessage(userText))

	out, err := r.model.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Content), nil
}
