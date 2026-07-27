package orchestration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"companion-ai/internal/conversation"
	"companion-ai/internal/memory"
	"companion-ai/internal/persona"
)

var (
	ErrInvalidRequest       = errors.New("invalid request")
	ErrMessageTooLarge      = errors.New("message too large")
	ErrConversationNotFound = errors.New("conversation not found")
	ErrNotStarted           = errors.New("conversation generation not started")
)

type Application struct {
	store *conversation.RedisStore
	repo  *memory.Repo
	model Model
	log   *slog.Logger
}

func NewApplication(store *conversation.RedisStore, repo *memory.Repo, model Model) *Application {
	return &Application{store: store, repo: repo, model: model, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func (a *Application) WithLogger(log *slog.Logger) *Application {
	if log != nil {
		a.log = log
	}
	return a
}

func (a *Application) ListSpaces(_ context.Context, _ Owner) ([]Space, error) {
	return FixedSpaces(), nil
}

func (a *Application) ListMessages(ctx context.Context, scope Scope) ([]Message, error) {
	if _, ok := FindSpace(scope.ConversationID); !ok {
		return nil, ErrConversationNotFound
	}
	messages, err := a.store.Messages(ctx, storageScope(scope))
	if err != nil {
		return nil, err
	}
	return normalizeCharacterMessages(messages), nil
}

func (a *Application) ClearToday(ctx context.Context, scope Scope) error {
	if _, ok := FindSpace(scope.ConversationID); !ok {
		return ErrConversationNotFound
	}
	return a.store.ClearScopeToday(ctx, storageScope(scope))
}

func (a *Application) Send(ctx context.Context, command SendCommand) (ResponseBatch, error) {
	startedAt := time.Now()
	space, ok := FindSpace(command.Scope.ConversationID)
	if !ok {
		return ResponseBatch{}, ErrConversationNotFound
	}
	content := strings.TrimSpace(command.Content)
	if content == "" || uuid.Validate(command.ClientRequestID) != nil {
		return ResponseBatch{}, ErrInvalidRequest
	}
	if utf8.RuneCountInString(content) > 4000 {
		return ResponseBatch{}, ErrMessageTooLarge
	}
	history, err := a.store.Messages(ctx, storageScope(command.Scope))
	if err != nil {
		return ResponseBatch{}, fmt.Errorf("%w: load history: %v", ErrNotStarted, err)
	}
	history = normalizeCharacterMessages(history)
	summaryTexts := []string(nil)
	if a.repo != nil {
		summaries, err := a.repo.RecentSummariesForScope(command.Scope.Owner.Channel, command.Scope.Owner.ID, command.Scope.ConversationID)
		if err != nil {
			return ResponseBatch{}, fmt.Errorf("%w: load summaries: %v", ErrNotStarted, err)
		}
		for _, summary := range summaries {
			summaryTexts = append(summaryTexts, summary.Content)
		}
	}
	batch, beginState, leaseToken, err := a.store.BeginBatch(ctx, storageScope(command.Scope), command.ClientRequestID, content)
	if err != nil {
		return ResponseBatch{}, err
	}
	if beginState == conversation.BeginExisting {
		a.logBatch(space, command.Scope, batch, 0, time.Since(startedAt))
		return batch, nil
	}
	settled := false
	defer func() {
		if settled {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		interrupted, cleanupErr := a.store.InterruptBatch(
			cleanupCtx, storageScope(command.Scope), batch.BatchID, batch.ClientRequestID, leaseToken, batch.CreatedAt,
		)
		if cleanupErr != nil {
			a.log.Warn("conversation batch cleanup failed",
				"conversation_kind", string(space.Kind), "owner_hash", ownerHash(command.Scope.Owner),
				"batch_id", batch.BatchID, "err", cleanupErr)
			return
		}
		a.logBatch(space, command.Scope, interrupted, len(interrupted.CharacterMessages), time.Since(startedAt))
	}()
	var plan []persona.CharacterID
	if space.Kind == SpaceDirect && len(space.Participants) == 1 {
		plan = []persona.CharacterID{space.Participants[0].ID}
	} else {
		planStartedAt := time.Now()
		plan = a.planSpeakers(ctx, command.Scope, space, history, content)
		a.log.Info("conversation speaker plan completed",
			"conversation_kind", string(space.Kind), "owner_hash", ownerHash(command.Scope.Owner),
			"batch_id", batch.BatchID, "selected_speaker_ids", characterIDs(plan),
			"planner_calls", 1, "latency_ms", time.Since(planStartedAt).Milliseconds())
	}
	batch.PlannedSpeakerIDs = make([]string, 0, len(plan))
	for _, id := range plan {
		batch.PlannedSpeakerIDs = append(batch.PlannedSpeakerIDs, string(id))
	}
	if err := a.store.SaveBatch(ctx, storageScope(command.Scope), &batch, leaseToken, false); err != nil {
		return ResponseBatch{}, err
	}
	for _, id := range plan {
		character, _ := persona.Find(id)
		prefix := append([]Message(nil), batch.CharacterMessages...)
		selectedHistory := selectHistoryBatches(history, character.SystemPrompt, content, prefix, summaryTexts)
		generationStartedAt := time.Now()
		text, generateErr := a.model.Generate(ctx, CharacterInput{
			Scope: command.Scope, Character: character, History: selectedHistory,
			Summaries: summaryTexts, UserMessage: batch.UserMessage, Prefix: prefix,
		})
		a.log.Info("conversation character generation completed",
			"conversation_kind", string(space.Kind), "owner_hash", ownerHash(command.Scope.Owner),
			"batch_id", batch.BatchID, "speaker_id", string(id),
			"latency_ms", time.Since(generationStartedAt).Milliseconds(), "success", generateErr == nil && strings.TrimSpace(text) != "")
		text = strings.TrimSpace(text)
		if generateErr != nil || text == "" {
			if len(batch.CharacterMessages) == 0 {
				batch.Status = conversation.BatchFailed
			} else {
				batch.Status = conversation.BatchPartial
			}
			batch.InterruptionCode = "generation_interrupted"
			if err := a.store.SaveBatch(ctx, storageScope(command.Scope), &batch, leaseToken, true); err != nil {
				return ResponseBatch{}, err
			}
			settled = true
			modelCalls := len(batch.CharacterMessages) + 1
			if space.Kind == SpaceGroup {
				modelCalls++
			}
			a.logBatch(space, command.Scope, batch, modelCalls, time.Since(startedAt))
			return batch, nil
		}
		if _, err := a.store.AppendCharacter(ctx, storageScope(command.Scope), &batch, leaseToken, string(character.ID), character.DisplayName, character.AvatarURL, text); err != nil {
			return ResponseBatch{}, err
		}
	}
	batch.Status = conversation.BatchComplete
	if err := a.store.SaveBatch(ctx, storageScope(command.Scope), &batch, leaseToken, true); err != nil {
		return ResponseBatch{}, err
	}
	settled = true
	modelCalls := len(batch.CharacterMessages)
	if space.Kind == SpaceGroup {
		modelCalls++
	}
	a.logBatch(space, command.Scope, batch, modelCalls, time.Since(startedAt))
	return batch, nil
}

func (a *Application) logBatch(space Space, scope Scope, batch ResponseBatch, modelCalls int, elapsed time.Duration) {
	a.log.Info("conversation batch finished",
		"conversation_kind", string(space.Kind), "owner_hash", ownerHash(scope.Owner),
		"batch_id", batch.BatchID, "selected_speaker_ids", append([]string(nil), batch.PlannedSpeakerIDs...),
		"status", batch.Status, "model_call_count", modelCalls, "latency_ms", elapsed.Milliseconds())
}

func ownerHash(owner Owner) string {
	sum := sha256.Sum256([]byte(owner.Channel + "\x00" + owner.ID))
	return fmt.Sprintf("%x", sum[:6])
}

func characterIDs(ids []persona.CharacterID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

func storageScope(scope Scope) conversation.Scope {
	return conversation.Scope{
		Identity:       conversation.Identity{Channel: scope.Owner.Channel, ExternalID: scope.Owner.ID},
		ConversationID: scope.ConversationID,
	}
}
