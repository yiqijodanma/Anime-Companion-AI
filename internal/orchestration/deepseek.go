package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/schema"

	"companion-ai/internal/chat"
	"companion-ai/internal/conversation"
	"companion-ai/internal/persona"
)

type DeepSeekAdapter struct {
	model chat.Model
}

const characterReplyInstruction = "只输出当前角色自己的发言正文。不要添加角色名、角色 ID 或冒号前缀，不要替其他角色发言，也不要续写多人对话。"

var speakerHeaderPattern = regexp.MustCompile(buildSpeakerHeaderPattern())

func NewDeepSeekAdapter(model chat.Model) *DeepSeekAdapter { return &DeepSeekAdapter{model: model} }

func (a *DeepSeekAdapter) Plan(ctx context.Context, input PlanInput) ([]persona.CharacterID, error) {
	participants := make([]string, 0, len(input.Space.Participants))
	for _, participant := range input.Space.Participants {
		participants = append(participants, string(participant.ID)+"="+participant.Description)
	}
	selectedHistory := selectHistoryBatches(input.History, "speaker planner", input.UserContent, nil, nil)
	var request strings.Builder
	if len(selectedHistory) > 0 {
		request.WriteString("最近群聊：\n")
		for _, message := range selectedHistory {
			request.WriteString(message.DisplayName)
			request.WriteString("(")
			request.WriteString(message.SpeakerID)
			request.WriteString("): ")
			request.WriteString(message.Content)
			request.WriteByte('\n')
		}
	}
	if len(input.AddressedIDs) > 0 {
		request.WriteString("用户明确提及：")
		request.WriteString(strings.Join(characterIDs(input.AddressedIDs), ","))
		request.WriteByte('\n')
	}
	request.WriteString("当前用户消息：")
	request.WriteString(input.UserContent)
	messages := []*schema.Message{
		schema.SystemMessage("选择本轮有必要发言的角色。只返回 1 到 5 个唯一角色 ID 的 JSON 数组，不要解释。可选：" + strings.Join(participants, "；")),
		schema.UserMessage(request.String()),
	}
	out, err := a.model.Generate(ctx, messages)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("invalid planner output")
	}
	raw := strings.TrimSpace(out.Content)
	if len(raw) == 0 || len(raw) > 1024 {
		return nil, fmt.Errorf("invalid planner output")
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("invalid planner output")
	}
	planned := make([]persona.CharacterID, 0, len(ids))
	for _, id := range ids {
		planned = append(planned, persona.CharacterID(id))
	}
	return planned, nil
}

func (a *DeepSeekAdapter) Generate(ctx context.Context, input CharacterInput) (string, error) {
	messages := []*schema.Message{schema.SystemMessage(input.Character.SystemPrompt + "\n\n" + characterReplyInstruction)}
	if len(input.Summaries) > 0 {
		messages = append(messages, schema.SystemMessage("本会话最近记忆：\n"+strings.Join(input.Summaries, "\n")))
	}
	for _, message := range input.History {
		messages = append(messages, modelMessage(message))
	}
	messages = append(messages, schema.UserMessage(input.UserMessage.Content))
	for _, message := range input.Prefix {
		messages = append(messages, schema.AssistantMessage(message.DisplayName+"("+message.SpeakerID+"): "+message.Content, nil))
	}
	out, err := a.model.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", fmt.Errorf("empty model output")
	}
	return normalizeCharacterReply(out.Content, input.Character), nil
}

func buildSpeakerHeaderPattern() string {
	labels := make([]string, 0, len(persona.All())*2)
	for _, character := range persona.All() {
		labels = append(labels, regexp.QuoteMeta(character.DisplayName), regexp.QuoteMeta(string(character.ID)))
	}
	return `(?m)^[\t ]*(` + strings.Join(labels, "|") + `)[\t ]*(\([^\r\n)]*\)|（[^\r\n）]*）)?[\t ]*[:：][\t ]*`
}

func normalizeCharacterReply(raw string, character persona.Character) string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	matches := speakerHeaderPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	for i, match := range matches {
		if !isCharacterLabel(text[match[2]:match[3]], character) {
			continue
		}
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		return stripRepeatedCharacterHeaders(text[match[1]:end], character)
	}
	return strings.TrimSpace(text[:matches[0][0]])
}

func normalizeCharacterMessages(messages []Message) []Message {
	clean := append([]Message(nil), messages...)
	for i := range clean {
		if clean[i].SpeakerKind != conversation.SpeakerCharacter {
			continue
		}
		character, ok := persona.Find(persona.CharacterID(clean[i].SpeakerID))
		if !ok {
			continue
		}
		clean[i].Content = normalizeCharacterReply(clean[i].Content, character)
	}
	return clean
}

func stripRepeatedCharacterHeaders(text string, character persona.Character) string {
	text = strings.TrimSpace(text)
	for {
		match := speakerHeaderPattern.FindStringSubmatchIndex(text)
		if match == nil || match[0] != 0 || !isCharacterLabel(text[match[2]:match[3]], character) {
			return text
		}
		text = strings.TrimSpace(text[match[1]:])
	}
}

func isCharacterLabel(label string, character persona.Character) bool {
	return label == character.DisplayName || label == string(character.ID)
}

func (a *DeepSeekAdapter) Summarize(ctx context.Context, input SummaryInput) (string, error) {
	var text strings.Builder
	for _, message := range input.Messages {
		text.WriteString(message.SpeakerID)
		text.WriteString(": ")
		text.WriteString(message.Content)
		text.WriteByte('\n')
	}
	out, err := a.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage("只提炼用户明确陈述的重要事实和事件，并保留说话者归属。"),
		schema.UserMessage(text.String()),
	})
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", fmt.Errorf("empty model output")
	}
	return strings.TrimSpace(out.Content), nil
}

func modelMessage(message conversation.Turn) *schema.Message {
	if message.SpeakerKind == conversation.SpeakerUser || message.Role == conversation.RoleUser {
		return schema.UserMessage(message.Content)
	}
	return schema.AssistantMessage(message.DisplayName+"("+message.SpeakerID+"): "+message.Content, nil)
}
