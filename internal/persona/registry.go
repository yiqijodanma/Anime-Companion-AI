package persona

import (
	"sort"
	"strings"
)

type CharacterID string

const (
	Haruhi  CharacterID = "haruhi"
	Kyon    CharacterID = "kyon"
	Yuki    CharacterID = "yuki"
	Mikuru  CharacterID = "mikuru"
	Koizumi CharacterID = "koizumi"
)

type Character struct {
	ID           CharacterID
	DisplayName  string
	Aliases      []string
	AvatarURL    string
	Description  string
	SystemPrompt string
}

var registry = []Character{
	{Haruhi, "凉宫春日", []string{"春日", "凉宫", "凉宫春日", "团长"}, "/app/assets/avatars/haruhi.svg", "SOS 团团长，行动力十足。", SystemPrompt},
	{Kyon, "阿虚", []string{"阿虚"}, "/app/assets/avatars/kyon.svg", "冷静吐槽、善于从常识角度观察。", "你是阿虚。用中文，以克制、略带吐槽但可靠的口吻回应；只代表自己发言。"},
	{Yuki, "长门有希", []string{"有希", "长门", "长门有希"}, "/app/assets/avatars/yuki.svg", "安静、精确，擅长分析信息。", "你是长门有希。用中文，言简意赅、冷静精确；只代表自己发言。"},
	{Mikuru, "朝比奈实玖瑠", []string{"实玖瑠", "朝比奈", "朝比奈实玖瑠"}, "/app/assets/avatars/mikuru.svg", "温柔体贴，偶尔有些慌张。", "你是朝比奈实玖瑠。用中文，温柔体贴、略显拘谨；只代表自己发言。"},
	{Koizumi, "古泉一树", []string{"古泉", "古泉一树"}, "/app/assets/avatars/koizumi.svg", "礼貌从容，善于分析和调和讨论。", "你是古泉一树。用中文，礼貌从容、善于分析；只代表自己发言。"},
}

func All() []Character {
	out := make([]Character, len(registry))
	copy(out, registry)
	for i := range out {
		out[i].Aliases = append([]string(nil), out[i].Aliases...)
	}
	return out
}

func Find(id CharacterID) (Character, bool) {
	for _, character := range registry {
		if character.ID == id {
			character.Aliases = append([]string(nil), character.Aliases...)
			return character, true
		}
	}
	return Character{}, false
}

func ResolveAliases(text string) []CharacterID {
	type mention struct {
		id    CharacterID
		index int
		order int
	}
	mentions := make([]mention, 0, len(registry))
	for order, character := range registry {
		first := -1
		for _, alias := range character.Aliases {
			if index := strings.Index(text, alias); index >= 0 && (first < 0 || index < first) {
				first = index
			}
		}
		if first >= 0 {
			mentions = append(mentions, mention{id: character.ID, index: first, order: order})
		}
	}
	sort.SliceStable(mentions, func(i, j int) bool {
		if mentions[i].index == mentions[j].index {
			return mentions[i].order < mentions[j].order
		}
		return mentions[i].index < mentions[j].index
	})
	out := make([]CharacterID, 0, len(mentions))
	for _, item := range mentions {
		out = append(out, item.id)
	}
	return out
}
