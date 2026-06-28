package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

type fakeModel struct {
	got []*schema.Message
}

func (f *fakeModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.got = msgs
	return schema.AssistantMessage("哼，本团长收到了！", nil), nil
}

func TestReplyBuildsPersonaContext(t *testing.T) {
	fm := &fakeModel{}
	r := NewReplier(fm)

	out, err := r.Reply(context.Background(),
		[]string{"昨天聊了棒球比赛"},
		[]Turn{{Role: "user", Content: "早上好"}, {Role: "assistant", Content: "早，团员！"}},
		"今天去哪探险")
	require.NoError(t, err)
	require.Equal(t, "哼，本团长收到了！", out)

	require.Equal(t, schema.System, fm.got[0].Role)
	require.True(t, strings.Contains(fm.got[0].Content, "凉宫春日"))

	joined := ""
	for _, m := range fm.got {
		joined += string(m.Role) + ":" + m.Content + "\n"
	}
	require.Contains(t, joined, "昨天聊了棒球比赛")
	require.Contains(t, joined, "早上好")

	last := fm.got[len(fm.got)-1]
	require.Equal(t, schema.User, last.Role)
	require.Equal(t, "今天去哪探险", last.Content)
}
