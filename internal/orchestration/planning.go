package orchestration

import (
	"context"

	"companion-ai/internal/persona"
)

func (a *Application) planSpeakers(ctx context.Context, scope Scope, space Space, history []Message, content string) []persona.CharacterID {
	addressed := persona.ResolveAliases(content)
	planned, err := a.model.Plan(ctx, PlanInput{
		Scope: scope, Space: space, History: history, UserContent: content, AddressedIDs: addressed,
	})
	if err != nil {
		planned = nil
	}
	allowed := make(map[persona.CharacterID]struct{}, len(space.Participants))
	for _, participant := range space.Participants {
		allowed[participant.ID] = struct{}{}
	}
	seen := make(map[persona.CharacterID]struct{}, len(planned))
	clean := make([]persona.CharacterID, 0, len(planned))
	for _, id := range planned {
		if len(clean) == 5 {
			break
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) > 0 {
		return clean
	}
	for _, id := range addressed {
		if _, ok := allowed[id]; ok {
			return []persona.CharacterID{id}
		}
	}
	return []persona.CharacterID{persona.Haruhi}
}
