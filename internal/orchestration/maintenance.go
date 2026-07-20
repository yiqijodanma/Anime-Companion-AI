package orchestration

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"companion-ai/internal/memory"
)

func (a *Application) MaintainDay(ctx context.Context, day time.Time) (MaintenanceResult, error) {
	result := MaintenanceResult{TargetDate: day}
	if a.repo == nil {
		return result, errors.New("memory repository unavailable")
	}
	storageScopes, err := a.store.ActiveScopes(ctx, day)
	if err != nil {
		return result, err
	}
	greet := make(map[string]struct{})
	for _, storage := range storageScopes {
		scope := Scope{
			Owner:          Owner{Channel: storage.Identity.Channel, ID: storage.Identity.ExternalID},
			ConversationID: storage.ConversationID,
		}
		messages, err := a.store.MessagesForDateScope(ctx, storage, day)
		if err != nil {
			result.Failed++
			continue
		}
		if len(messages) == 0 {
			if err := a.store.ClearScopeDate(ctx, storage, day); err != nil {
				result.Failed++
				continue
			}
			result.Processed++
			continue
		}
		archive := make([]memory.ArchiveTurn, 0, len(messages))
		for _, message := range messages {
			archive = append(archive, memory.ArchiveTurn{
				TurnID: message.TurnID, Role: message.Role, Content: message.Content, CreatedAt: message.CreatedAt,
				SpeakerKind: message.SpeakerKind, SpeakerID: message.SpeakerID, BatchID: message.BatchID, Sequence: message.Sequence,
			})
		}
		if err := a.repo.ArchiveDailyConversationForScope(
			scope.Owner.Channel, scope.Owner.ID, scope.ConversationID, day, archive, "",
		); err != nil {
			result.Failed++
			continue
		}
		summary, err := a.model.Summarize(ctx, SummaryInput{Scope: scope, Messages: messages})
		if err != nil || strings.TrimSpace(summary) == "" {
			result.Failed++
			continue
		}
		if err := a.repo.ArchiveDailyConversationForScope(
			scope.Owner.Channel, scope.Owner.ID, scope.ConversationID, day, nil, strings.TrimSpace(summary),
		); err != nil {
			result.Failed++
			continue
		}
		if err := a.store.ClearScopeDate(ctx, storage, day); err != nil {
			result.Failed++
			continue
		}
		result.Processed++
		if scope.Owner.Channel == "wechat" {
			greet[scope.Owner.ID] = struct{}{}
		}
	}
	for owner := range greet {
		result.GreetOwnerIDs = append(result.GreetOwnerIDs, owner)
	}
	sort.Strings(result.GreetOwnerIDs)
	if err := a.repo.PurgeSummariesOlderThan(day.AddDate(0, 0, -7)); err != nil {
		result.Failed++
	}
	return result, nil
}
