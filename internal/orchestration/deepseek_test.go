package orchestration

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"companion-ai/internal/conversation"
	"companion-ai/internal/persona"
)

type scriptedChatModel struct {
	output string
}

func (m *scriptedChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.output, nil), nil
}

func TestDeepSeekAdapterGenerateKeepsOnlyCurrentCharacterReply(t *testing.T) {
	tests := []struct {
		name      string
		character persona.CharacterID
		output    string
		want      string
	}{
		{
			name:      "strips speaker prefix and later character blocks",
			character: persona.Haruhi,
			output: `凉宫春日(haruhi): 哈！来得正好！今天放学后我们要去调查一个超有趣的都市传说！

阿虚(kyon): (叹气) 我就知道会是这样……

朝比奈实玖瑠(mikuru): 呜……又是那种阴森森的地方吗……

古泉一树(koizumi): 呵呵，我可是很期待哦。`,
			want: "哈！来得正好！今天放学后我们要去调查一个超有趣的都市传说！",
		},
		{
			name:      "extracts current character when another character appears first",
			character: persona.Koizumi,
			output: `凉宫春日(haruhi): 凉宫春日(haruhi): 很好！古泉，帮我背包！

阿虚(kyon): 都说了那地图是假的……

古泉一树(koizumi): 凉宫同学还真是充满干劲呢。不过新来的同学，我建议你不用太担心。`,
			want: "凉宫同学还真是充满干劲呢。不过新来的同学，我建议你不用太担心。",
		},
		{
			name:      "strips a mismatched parenthesized id from the current display name",
			character: persona.Koizumi,
			output:    "古泉一树(kyon-kun): 欢迎加入SOS团。",
			want:      "欢迎加入SOS团。",
		},
		{
			name:      "preserves an unlabeled multiline reply",
			character: persona.Mikuru,
			output:    "（听到要被分配任务，显得有些慌乱）\n诶？！让、让我负责急救装备吗……好的！",
			want:      "（听到要被分配任务，显得有些慌乱）\n诶？！让、让我负责急救装备吗……好的！",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			character, ok := persona.Find(tt.character)
			require.True(t, ok)
			adapter := NewDeepSeekAdapter(&scriptedChatModel{output: tt.output})

			got, err := adapter.Generate(context.Background(), CharacterInput{Character: character})

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeCharacterMessagesCleansHistoryWithoutMutatingStoredValues(t *testing.T) {
	stored := []Message{
		{
			Role: conversation.RoleAssistant, SpeakerKind: conversation.SpeakerCharacter, SpeakerID: string(persona.Haruhi),
			Content: "凉宫春日(haruhi): 出发！\n\n阿虚(kyon): 我就知道……",
		},
		{
			Role: conversation.RoleAssistant, SpeakerKind: conversation.SpeakerCharacter, SpeakerID: string(persona.Koizumi),
			Content: "凉宫春日(haruhi): 快跟上！\n\n古泉一树(koizumi): 呵呵，那就出发吧。",
		},
		{Role: conversation.RoleUser, SpeakerKind: conversation.SpeakerUser, Content: "凉宫春日(haruhi): 这只是用户输入"},
	}

	clean := normalizeCharacterMessages(stored)

	require.Equal(t, "出发！", clean[0].Content)
	require.Equal(t, "呵呵，那就出发吧。", clean[1].Content)
	require.Equal(t, stored[2].Content, clean[2].Content)
	require.Contains(t, stored[0].Content, "阿虚(kyon):")
}
