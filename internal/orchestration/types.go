package orchestration

import (
	"context"
	"time"

	"companion-ai/internal/conversation"
	"companion-ai/internal/persona"
)

type Owner struct {
	Channel string
	ID      string
}

type Scope struct {
	Owner          Owner
	ConversationID string
}

type SpaceKind string

const (
	SpaceGroup  SpaceKind = "group"
	SpaceDirect SpaceKind = "direct"
)

type Space struct {
	ID           string
	Kind         SpaceKind
	DisplayName  string
	Participants []persona.Character
}

type Message = conversation.Turn
type ResponseBatch = conversation.Batch

type SendCommand struct {
	Scope           Scope
	Content         string
	ClientRequestID string
}

type PlanInput struct {
	Scope        Scope
	Space        Space
	History      []Message
	UserContent  string
	AddressedIDs []persona.CharacterID
}

type CharacterInput struct {
	Scope       Scope
	Character   persona.Character
	Summaries   []string
	History     []Message
	UserMessage Message
	Prefix      []Message
}

type SummaryInput struct {
	Scope    Scope
	Messages []Message
}

type MaintenanceResult struct {
	GreetOwnerIDs []string
	Processed     int
	Failed        int
	TargetDate    time.Time
}

type Model interface {
	Plan(context.Context, PlanInput) ([]persona.CharacterID, error)
	Generate(context.Context, CharacterInput) (string, error)
	Summarize(context.Context, SummaryInput) (string, error)
}

func FixedSpaces() []Space {
	characters := persona.All()
	spaces := []Space{{ID: "sos-group", Kind: SpaceGroup, DisplayName: "SOS 团", Participants: characters}}
	for _, character := range characters {
		spaces = append(spaces, Space{
			ID: "direct-" + string(character.ID), Kind: SpaceDirect,
			DisplayName: character.DisplayName, Participants: []persona.Character{character},
		})
	}
	return spaces
}

func FindSpace(id string) (Space, bool) {
	for _, space := range FixedSpaces() {
		if space.ID == id {
			return space, true
		}
	}
	return Space{}, false
}
