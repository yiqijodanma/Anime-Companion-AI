package summarize

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"companion-ai/internal/chat"
)

type fakeModel struct {
	got []*schema.Message
}

func (f *fakeModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.got = msgs
	return schema.AssistantMessage("用户今天想去海边，也聊了 SOS 团。", nil), nil
}

func TestSummarizeBuildsConversationInput(t *testing.T) {
	fm := &fakeModel{}
	s := NewSummarizer(fm)

	out, err := s.Summarize(context.Background(), []chat.Turn{
		{Role: "user", Content: "我想去海边"},
		{Role: "assistant", Content: "那就出发！"},
	})
	require.NoError(t, err)
	require.Equal(t, "用户今天想去海边，也聊了 SOS 团。", out)
	require.Len(t, fm.got, 2)
	require.Equal(t, schema.System, fm.got[0].Role)

	joined := ""
	for _, m := range fm.got {
		joined += m.Content + "\n"
	}
	require.True(t, strings.Contains(joined, "我想去海边"))
	require.True(t, strings.Contains(joined, "assistant: 那就出发！"))
}

func TestSummarizeEmptyReturnsEmpty(t *testing.T) {
	s := NewSummarizer(&fakeModel{})
	out, err := s.Summarize(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "", out)
}
