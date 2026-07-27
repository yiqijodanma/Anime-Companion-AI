package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	authn "companion-ai/internal/auth"
	"companion-ai/internal/conversation"
	"companion-ai/internal/gateway"
	"companion-ai/internal/orchestration"
	"companion-ai/internal/persona"
	"companion-ai/internal/quota"
)

type listedSpace struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Participants []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		AvatarURL   string `json:"avatar_url"`
	} `json:"participants"`
}

type scriptedConversationModel struct {
	mu            sync.Mutex
	plan          []persona.CharacterID
	planInputs    []orchestration.PlanInput
	replies       map[persona.CharacterID]string
	planCalls     int
	generateCalls []orchestration.CharacterInput
	generate      func(orchestration.CharacterInput) (string, error)
}

type cancelingPlanModel struct {
	started chan struct{}
	once    sync.Once
}

func (m *cancelingPlanModel) Plan(ctx context.Context, _ orchestration.PlanInput) ([]persona.CharacterID, error) {
	m.once.Do(func() { close(m.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*cancelingPlanModel) Generate(context.Context, orchestration.CharacterInput) (string, error) {
	return "", errors.New("generation must not start after request cancellation")
}

func (*cancelingPlanModel) Summarize(context.Context, orchestration.SummaryInput) (string, error) {
	return "", nil
}

func (m *scriptedConversationModel) Plan(_ context.Context, input orchestration.PlanInput) ([]persona.CharacterID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.planCalls++
	m.planInputs = append(m.planInputs, input)
	return append([]persona.CharacterID(nil), m.plan...), nil
}

func (m *scriptedConversationModel) Generate(_ context.Context, input orchestration.CharacterInput) (string, error) {
	m.mu.Lock()
	m.generateCalls = append(m.generateCalls, input)
	generate := m.generate
	reply := m.replies[input.Character.ID]
	m.mu.Unlock()
	if generate != nil {
		return generate(input)
	}
	return reply, nil
}

func (m *scriptedConversationModel) Summarize(context.Context, orchestration.SummaryInput) (string, error) {
	return "", nil
}

func TestAuthenticatedUserListsSixConversationSpaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokens := []string{"token-owner-1", "token-owner-2"}

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(grpcServer, agent.NewServer(nil, nil, nil, nil))
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	router := gin.New()
	(&gateway.Handlers{
		Agent: gateway.NewAgentClient(conn),
		AuthenticateSession: func(_ context.Context, token string) (authn.User, error) {
			return authn.User{ID: token[len("token-"):]}, nil
		},
	}).RegisterRoutes(router)

	wantIDs := []string{"sos-group", "direct-haruhi", "direct-kyon", "direct-yuki", "direct-mikuru", "direct-koizumi"}
	for _, token := range tokens {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations?owner_id=spoofed", nil)
		req.AddCookie(&http.Cookie{Name: "sos_session", Value: token})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body struct {
			Conversations []listedSpace `json:"conversations"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Conversations, 6)
		ids := make([]string, 0, len(body.Conversations))
		for _, space := range body.Conversations {
			ids = append(ids, space.ID)
			require.NotEmpty(t, space.Kind)
			require.NotEmpty(t, space.Participants)
			for _, participant := range space.Participants {
				require.NotEmpty(t, participant.ID)
				require.NotEmpty(t, participant.DisplayName)
				require.NotEmpty(t, participant.AvatarURL)
			}
		}
		require.Equal(t, wantIDs, ids)
		require.Equal(t, "group", body.Conversations[0].Kind)
		require.Len(t, body.Conversations[0].Participants, 5)
		for _, space := range body.Conversations[1:] {
			require.Equal(t, "direct", space.Kind)
			require.Len(t, space.Participants, 1)
		}
	}
}

func TestDirectHaruhiReturnsStructuredBatchAndReloadableMessages(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "当然要听本团长的！"}}
	router := newConversationRESTRouter(t, model)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", strings.NewReader(`{"content":"我们今天做什么？","client_request_id":"c0a80101-0000-4000-8000-000000000001"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "sos_session", Value: "token-owner-1"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var sent struct {
		Batch struct {
			BatchID           string                        `json:"batch_id"`
			ConversationID    string                        `json:"conversation_id"`
			Status            string                        `json:"status"`
			UserMessage       gateway.ConversationMessage   `json:"user_message"`
			CharacterMessages []gateway.ConversationMessage `json:"character_messages"`
		} `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sent))
	require.NotEmpty(t, sent.Batch.BatchID)
	require.Equal(t, "direct-haruhi", sent.Batch.ConversationID)
	require.Equal(t, "complete", sent.Batch.Status)
	require.Equal(t, "user", sent.Batch.UserMessage.SpeakerKind)
	require.Equal(t, uint64(1), sent.Batch.UserMessage.Sequence)
	require.Len(t, sent.Batch.CharacterMessages, 1)
	reply := sent.Batch.CharacterMessages[0]
	require.Equal(t, "haruhi", reply.SpeakerID)
	require.Equal(t, sent.Batch.BatchID, reply.BatchID)
	require.Equal(t, uint64(2), reply.Sequence)
	require.Equal(t, "当然要听本团长的！", reply.Content)
	require.Equal(t, 0, model.planCalls, "direct chat must bypass the group planner")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/direct-haruhi/messages", nil)
	getReq.AddCookie(&http.Cookie{Name: "sos_session", Value: "token-owner-1"})
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code, getW.Body.String())
	var history struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &history))
	require.Len(t, history.Messages, 2)
	require.Equal(t, []uint64{1, 2}, []uint64{history.Messages[0].Sequence, history.Messages[1].Sequence})
	require.Equal(t, []string{"user", "haruhi"}, []string{history.Messages[0].SpeakerID, history.Messages[1].SpeakerID})
}

func TestDirectSpacesOwnershipClearOneSpaceAndLegacyRedis(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{
		persona.Haruhi: "春日回复", persona.Kyon: "阿虚回复", persona.Yuki: "有希回复",
		persona.Mikuru: "实玖瑠回复", persona.Koizumi: "古泉回复",
	}}
	fixture := newConversationRESTFixture(t, model)
	directs := []struct {
		space   string
		speaker string
	}{
		{"direct-haruhi", "haruhi"}, {"direct-kyon", "kyon"}, {"direct-yuki", "yuki"},
		{"direct-mikuru", "mikuru"}, {"direct-koizumi", "koizumi"},
	}
	for i, direct := range directs {
		body := fmt.Sprintf(`{"content":"只在%s说话","client_request_id":"c0a80101-0000-4000-8000-%012d"}`, direct.space, i+10)
		w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/"+direct.space+"/messages", "owner-1", body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response struct {
			Batch gateway.ResponseBatch `json:"batch"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Batch.CharacterMessages, 1)
		require.Equal(t, direct.speaker, response.Batch.CharacterMessages[0].SpeakerID)
	}
	require.Equal(t, 0, model.planCalls)

	otherOwner := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/direct-haruhi/messages", "owner-2", "")
	require.Equal(t, http.StatusOK, otherOwner.Code)
	require.JSONEq(t, `{"messages":[]}`, otherOwner.Body.String())
	group := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/sos-group/messages", "owner-1", "")
	require.Equal(t, http.StatusOK, group.Code)
	require.JSONEq(t, `{"messages":[]}`, group.Body.String())

	cleared := authenticatedRequest(fixture.router, http.MethodDelete, "/api/v1/conversations/direct-yuki/messages", "owner-1", "")
	require.Equal(t, http.StatusNoContent, cleared.Code)
	clearedAgain := authenticatedRequest(fixture.router, http.MethodDelete, "/api/v1/conversations/direct-yuki/messages", "owner-1", "")
	require.Equal(t, http.StatusNoContent, clearedAgain.Code)
	yukiHistory := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/direct-yuki/messages", "owner-1", "")
	require.JSONEq(t, `{"messages":[]}`, yukiHistory.Body.String())
	haruhiHistory := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/direct-haruhi/messages", "owner-1", "")
	var kept struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(haruhiHistory.Body.Bytes(), &kept))
	require.Len(t, kept.Messages, 2)

	legacyOwner := conversation.Identity{Channel: "api", ExternalID: "owner-legacy"}
	_, err := fixture.store.AddTurn(context.Background(), legacyOwner, conversation.RoleUser, "旧版用户消息")
	require.NoError(t, err)
	_, err = fixture.store.AddTurn(context.Background(), legacyOwner, conversation.RoleAssistant, "旧版春日回复")
	require.NoError(t, err)
	legacy := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/direct-haruhi/messages", "owner-legacy", "")
	require.Equal(t, http.StatusOK, legacy.Code, legacy.Body.String())
	var migrated struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(legacy.Body.Bytes(), &migrated))
	require.Len(t, migrated.Messages, 2)
	require.Equal(t, []string{"user", "haruhi"}, []string{migrated.Messages[0].SpeakerID, migrated.Messages[1].SpeakerID})
	require.Equal(t, []uint64{1, 2}, []uint64{migrated.Messages[0].Sequence, migrated.Messages[1].Sequence})
}

func TestGroupOneSpeakerDoesNotCreateSilentAcknowledgements(t *testing.T) {
	model := &scriptedConversationModel{
		plan:    []persona.CharacterID{persona.Yuki},
		replies: map[persona.CharacterID]string{persona.Yuki: "先确认事实，再决定行动。"},
	}
	fixture := newConversationRESTFixture(t, model)
	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-1",
		`{"content":"有希，这个现象怎么看？","client_request_id":"c0a80101-0000-4000-8000-000000000100"}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "complete", response.Batch.Status)
	require.Equal(t, []string{"yuki"}, response.Batch.PlannedSpeakerIDs)
	require.Len(t, response.Batch.CharacterMessages, 1)
	require.Equal(t, "yuki", response.Batch.CharacterMessages[0].SpeakerID)
	require.NotContains(t, w.Body.String(), "rationale")
	require.Equal(t, 1, model.planCalls)

	history := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/sos-group/messages", "owner-1", "")
	require.Equal(t, http.StatusOK, history.Code)
	require.NotContains(t, history.Body.String(), "rationale")
	var listed struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &listed))
	require.Len(t, listed.Messages, 2, "silent members must not create acknowledgement messages")
}

func TestYukiKyonHaruhiSequentialContext(t *testing.T) {
	replies := map[persona.CharacterID]string{
		persona.Yuki:   "有希：观测数据支持这个结论。",
		persona.Kyon:   "阿虚：既然长门都这么说了，那大概没错。",
		persona.Haruhi: "春日：很好，那就照这个结论行动！",
	}
	expectedPrefix := map[persona.CharacterID][]persona.CharacterID{
		persona.Yuki: nil, persona.Kyon: {persona.Yuki}, persona.Haruhi: {persona.Yuki, persona.Kyon},
	}
	model := &scriptedConversationModel{plan: []persona.CharacterID{persona.Yuki, persona.Kyon, persona.Haruhi}}
	model.generate = func(input orchestration.CharacterInput) (string, error) {
		require.Equal(t, "大家根据观测结果讨论一下。", input.UserMessage.Content)
		want := expectedPrefix[input.Character.ID]
		require.Len(t, input.Prefix, len(want))
		for i, id := range want {
			require.Equal(t, string(id), input.Prefix[i].SpeakerID)
			require.Equal(t, replies[id], input.Prefix[i].Content)
		}
		return replies[input.Character.ID], nil
	}
	fixture := newConversationRESTFixture(t, model)
	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-1",
		`{"content":"大家根据观测结果讨论一下。","client_request_id":"c0a80101-0000-4000-8000-000000000200"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, []string{"yuki", "kyon", "haruhi"}, response.Batch.PlannedSpeakerIDs)
	require.Len(t, response.Batch.CharacterMessages, 3)
	for i, id := range []string{"yuki", "kyon", "haruhi"} {
		require.Equal(t, id, response.Batch.CharacterMessages[i].SpeakerID)
		require.Equal(t, response.Batch.BatchID, response.Batch.CharacterMessages[i].BatchID)
		require.Equal(t, uint64(i+2), response.Batch.CharacterMessages[i].Sequence)
	}

	history := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/sos-group/messages", "owner-1", "")
	var listed struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &listed))
	require.Len(t, listed.Messages, 4)
	require.Equal(t, []string{"user", "yuki", "kyon", "haruhi"}, []string{
		listed.Messages[0].SpeakerID, listed.Messages[1].SpeakerID, listed.Messages[2].SpeakerID, listed.Messages[3].SpeakerID,
	})
}

func TestContextBudgetKeepsOnlyCompleteRecentBatches(t *testing.T) {
	longUser := strings.Repeat("甲", 1500)
	longReply := strings.Repeat("乙", 1500)
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Yuki: longReply}}
	fixture := newConversationRESTFixture(t, model)
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf(`{"content":"%s","client_request_id":"c0a80101-0000-4000-8000-%012d"}`, longUser, i+300)
		w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-yuki/messages", "owner-budget", body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-yuki/messages", "owner-budget",
		`{"content":"最后一个问题","client_request_id":"c0a80101-0000-4000-8000-000000000399"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	last := model.generateCalls[len(model.generateCalls)-1]
	require.Less(t, len(last.History), 6, "history must be trimmed by budget")
	require.NotEmpty(t, last.History)
	require.Zero(t, len(last.History)%2, "a retained direct-chat batch must keep user and character together")
	for i := 0; i < len(last.History); i += 2 {
		require.Equal(t, last.History[i].BatchID, last.History[i+1].BatchID)
	}
}

func TestFiveSpeakersAliasesAndInvalidPlanAreSafe(t *testing.T) {
	model := &scriptedConversationModel{
		plan: []persona.CharacterID{
			persona.Yuki, persona.Yuki, persona.CharacterID("alien"), persona.Kyon,
			persona.Haruhi, persona.Mikuru, persona.Koizumi, persona.Haruhi,
		},
		replies: map[persona.CharacterID]string{
			persona.Yuki: "有希", persona.Kyon: "阿虚", persona.Haruhi: "春日",
			persona.Mikuru: "实玖瑠", persona.Koizumi: "古泉",
		},
	}
	model.generate = func(input orchestration.CharacterInput) (string, error) {
		wantIndex := map[persona.CharacterID]int{
			persona.Yuki: 0, persona.Kyon: 1, persona.Haruhi: 2, persona.Mikuru: 3, persona.Koizumi: 4,
		}[input.Character.ID]
		require.Len(t, input.Prefix, wantIndex)
		return model.replies[input.Character.ID], nil
	}
	fixture := newConversationRESTFixture(t, model)
	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-five",
		`{"content":"有希、团长、阿虚、朝比奈和古泉都说说吧","client_request_id":"c0a80101-0000-4000-8000-000000000400"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, []string{"yuki", "kyon", "haruhi", "mikuru", "koizumi"}, response.Batch.PlannedSpeakerIDs)
	require.Len(t, response.Batch.CharacterMessages, 5)
	require.Equal(t, []persona.CharacterID{persona.Yuki, persona.Haruhi, persona.Kyon, persona.Mikuru, persona.Koizumi}, model.planInputs[0].AddressedIDs)

	model.plan = nil
	fallback := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-fallback",
		`{"content":"长门，请回答","client_request_id":"c0a80101-0000-4000-8000-000000000401"}`)
	require.Equal(t, http.StatusOK, fallback.Code, fallback.Body.String())
	var fallbackResponse struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(fallback.Body.Bytes(), &fallbackResponse))
	require.Equal(t, []string{"yuki"}, fallbackResponse.Batch.PlannedSpeakerIDs)
}

func TestIdempotentCompletedRequestReturnsSameBatchWithoutDuplicateMessages(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "唯一回复"}}
	fixture := newConversationRESTFixture(t, model)
	body := `{"content":"只发送一次","client_request_id":"c0a80101-0000-4000-8000-000000000500"}`
	first := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "owner-idempotent", body)
	second := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "owner-idempotent", body)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var firstResponse, secondResponse struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResponse))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondResponse))
	require.Equal(t, firstResponse.Batch.BatchID, secondResponse.Batch.BatchID)
	require.Len(t, model.generateCalls, 1)
	history := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/direct-haruhi/messages", "owner-idempotent", "")
	var listed struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &listed))
	require.Len(t, listed.Messages, 2)
}

func TestConversationBusyAndDuplicateDuringGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "完成"}}
	model.generate = func(input orchestration.CharacterInput) (string, error) {
		once.Do(func() { close(started) })
		<-release
		return "完成", nil
	}
	fixture := newConversationRESTFixture(t, model)
	body := `{"content":"慢请求","client_request_id":"c0a80101-0000-4000-8000-000000000510"}`
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "owner-busy", body)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first generation did not start")
	}

	duplicate := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "owner-busy", body)
	require.Equal(t, http.StatusOK, duplicate.Code, duplicate.Body.String())
	var duplicateResponse struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(duplicate.Body.Bytes(), &duplicateResponse))
	require.Equal(t, "generating", duplicateResponse.Batch.Status)

	busy := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "owner-busy",
		`{"content":"另一个请求","client_request_id":"c0a80101-0000-4000-8000-000000000511"}`)
	require.Equal(t, http.StatusConflict, busy.Code, busy.Body.String())
	require.Contains(t, busy.Body.String(), "conversation_busy")
	close(release)
	first := <-firstDone
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstResponse struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResponse))
	require.Equal(t, duplicateResponse.Batch.BatchID, firstResponse.Batch.BatchID)
	model.mu.Lock()
	require.Len(t, model.generateCalls, 1)
	model.mu.Unlock()
}

func TestCanceledGroupPlanningFinalizesStartedBatch(t *testing.T) {
	model := &cancelingPlanModel{started: make(chan struct{})}
	fixture := newConversationRESTFixture(t, model)
	body := `{"content":"大家好","client_request_id":"c0a80101-0000-4000-8000-000000000515"}`

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/sos-group/messages", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "sos_session", Value: "token-owner-canceled"})
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		fixture.router.ServeHTTP(w, req)
		firstDone <- w
	}()

	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("group planner did not start")
	}
	cancel()
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled request did not return")
	}

	var response struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		retry := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-canceled", body)
		require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
		require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &response))
		if response.Batch.Status != "generating" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, "failed", response.Batch.Status)
	require.Equal(t, "generation_interrupted", response.Batch.InterruptionCode)
	require.Empty(t, response.Batch.CharacterMessages)
}

func TestDifferentSpacesCanGenerateConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "春日", persona.Yuki: "有希"}}
	model.generate = func(input orchestration.CharacterInput) (string, error) {
		started <- input.Scope.ConversationID
		<-release
		return model.replies[input.Character.ID], nil
	}
	fixture := newConversationRESTFixture(t, model)
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() {
		done <- authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "owner-parallel",
			`{"content":"春日窗口","client_request_id":"c0a80101-0000-4000-8000-000000000520"}`)
	}()
	go func() {
		done <- authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-yuki/messages", "owner-parallel",
			`{"content":"有希窗口","client_request_id":"c0a80101-0000-4000-8000-000000000521"}`)
	}()
	seen := map[string]bool{}
	for range 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(3 * time.Second):
			t.Fatal("different spaces did not enter generation concurrently")
		}
	}
	require.Equal(t, map[string]bool{"direct-haruhi": true, "direct-yuki": true}, seen)
	close(release)
	require.Equal(t, http.StatusOK, (<-done).Code)
	require.Equal(t, http.StatusOK, (<-done).Code)
}

func TestFirstSpeakerFailureAndEmptyOutputHaveNoGhostMessage(t *testing.T) {
	tests := []struct {
		name     string
		generate func(orchestration.CharacterInput) (string, error)
	}{
		{name: "provider error", generate: func(orchestration.CharacterInput) (string, error) {
			return "", errors.New("secret provider failure containing prompt")
		}},
		{name: "empty output", generate: func(orchestration.CharacterInput) (string, error) { return "  ", nil }},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &scriptedConversationModel{plan: []persona.CharacterID{persona.Yuki}, generate: tt.generate}
			fixture := newConversationRESTFixture(t, model)
			body := fmt.Sprintf(`{"content":"需要回答","client_request_id":"c0a80101-0000-4000-8000-%012d"}`, i+600)
			w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-failed", body)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var response struct {
				Batch gateway.ResponseBatch `json:"batch"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Equal(t, "failed", response.Batch.Status)
			require.Equal(t, "generation_interrupted", response.Batch.InterruptionCode)
			require.Empty(t, response.Batch.CharacterMessages)
			require.NotContains(t, w.Body.String(), "secret provider")
			history := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/sos-group/messages", "owner-failed", "")
			var listed struct {
				Messages []gateway.ConversationMessage `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(history.Body.Bytes(), &listed))
			require.Len(t, listed.Messages, 1)
			require.Equal(t, "user", listed.Messages[0].SpeakerID)
		})
	}
}

func TestPartialBatchKeepsPrefixAndNextTurnCanSeeIt(t *testing.T) {
	model := &scriptedConversationModel{plan: []persona.CharacterID{persona.Yuki, persona.Kyon, persona.Haruhi}}
	model.generate = func(input orchestration.CharacterInput) (string, error) {
		switch input.Character.ID {
		case persona.Yuki:
			return "有希已经成功回答", nil
		case persona.Kyon:
			return "", errors.New("private provider detail")
		default:
			t.Fatal("speakers after the failed prefix must not generate")
			return "", nil
		}
	}
	fixture := newConversationRESTFixture(t, model)
	first := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-partial",
		`{"content":"三个人讨论","client_request_id":"c0a80101-0000-4000-8000-000000000610"}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var response struct {
		Batch gateway.ResponseBatch `json:"batch"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &response))
	require.Equal(t, "partial", response.Batch.Status)
	require.Len(t, response.Batch.CharacterMessages, 1)
	require.Equal(t, "yuki", response.Batch.CharacterMessages[0].SpeakerID)
	require.NotContains(t, first.Body.String(), "private provider detail")

	model.mu.Lock()
	model.plan = []persona.CharacterID{persona.Haruhi}
	model.generate = func(input orchestration.CharacterInput) (string, error) {
		require.Len(t, input.History, 2)
		require.Equal(t, "有希已经成功回答", input.History[1].Content)
		return "春日看到了前一轮有希的回答", nil
	}
	model.mu.Unlock()
	next := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "owner-partial",
		`{"content":"继续","client_request_id":"c0a80101-0000-4000-8000-000000000611"}`)
	require.Equal(t, http.StatusOK, next.Code, next.Body.String())
}

func TestDeprecatedAuthenticatedWebAliasMapsToDirectHaruhi(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "旧接口仍由春日回答"}}
	fixture := newConversationRESTFixture(t, model)
	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/messages", "owner-alias", `{"content":"旧接口消息"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "true", w.Header().Get("Deprecation"))
	require.JSONEq(t, `{"reply":"旧接口仍由春日回答","quota":{"unlimited":false,"limit":20,"used":1,"remaining":19,"reset_at":"2026-07-24T00:00:00+08:00","revision":2}}`, w.Body.String())

	legacyList := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/messages", "owner-alias", "")
	require.Equal(t, http.StatusOK, legacyList.Code, legacyList.Body.String())
	var listed struct {
		Messages []gateway.ConversationMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(legacyList.Body.Bytes(), &listed))
	require.Len(t, listed.Messages, 2)
	require.Equal(t, "direct-haruhi", listed.Messages[1].ConversationID)
	require.Equal(t, "haruhi", listed.Messages[1].SpeakerID)

	deleted := authenticatedRequest(fixture.router, http.MethodDelete, "/api/v1/conversations/messages", "owner-alias", "")
	require.Equal(t, http.StatusNoContent, deleted.Code)
	newList := authenticatedRequest(fixture.router, http.MethodGet, "/api/v1/conversations/direct-haruhi/messages", "owner-alias", "")
	require.JSONEq(t, `{"messages":[]}`, newList.Body.String())
}

func TestDeprecatedAuthenticatedWebAliasPreservesGenerationFailureContract(t *testing.T) {
	model := &scriptedConversationModel{generate: func(orchestration.CharacterInput) (string, error) {
		return "", errors.New("provider detail must stay private")
	}}
	fixture := newConversationRESTFixture(t, model)

	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/messages", "owner-alias-failed", `{"content":"旧接口失败"}`)
	require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"code":"agent_error"`)
	require.NotContains(t, w.Body.String(), "provider detail")
}

func TestWechatLegacyReplyUsesDirectHaruhiWithoutGroupPlanner(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "微信春日回复"}}
	fixture := newConversationRESTFixture(t, model)
	reply, err := fixture.client.Reply(context.Background(), "wechat", "wechat-owner", "在吗")
	require.NoError(t, err)
	require.Equal(t, "微信春日回复", reply)
	require.Equal(t, 0, model.planCalls)
	messages, err := fixture.client.ListConversationMessages(context.Background(), "wechat", "wechat-owner", "direct-haruhi")
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "haruhi", messages[1].SpeakerID)
}

func TestLegacyAgentListAndDeleteUseDirectHaruhiWithOrchestration(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "兼容回复"}}
	fixture := newConversationRESTFixture(t, model)

	_, err := fixture.client.Reply(context.Background(), "api", "legacy-agent-owner", "兼容请求")
	require.NoError(t, err)

	messages, err := fixture.client.ListMessages(context.Background(), "api", "legacy-agent-owner")
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "direct-haruhi", messages[1].ConversationID)
	require.Equal(t, "haruhi", messages[1].SpeakerID)

	require.NoError(t, fixture.client.DeleteMessages(context.Background(), "api", "legacy-agent-owner"))
	messages, err = fixture.client.ListConversationMessages(context.Background(), "api", "legacy-agent-owner", "direct-haruhi")
	require.NoError(t, err)
	require.Empty(t, messages)
}

func TestSafeOperationalLoggingContainsMetadataNotConversationContent(t *testing.T) {
	model := &scriptedConversationModel{plan: []persona.CharacterID{persona.Yuki}}
	model.generate = func(orchestration.CharacterInput) (string, error) {
		return "", errors.New("provider-secret-detail")
	}
	fixture := newConversationRESTFixture(t, model)
	var logs bytes.Buffer
	fixture.app.WithLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	w := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "sensitive-owner",
		`{"content":"private-conversation-content","client_request_id":"c0a80101-0000-4000-8000-000000000700"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	text := logs.String()
	require.Contains(t, text, `"conversation_kind":"group"`)
	require.Contains(t, text, `"selected_speaker_ids"`)
	require.Contains(t, text, `"status":"failed"`)
	require.Contains(t, text, `"model_call_count":2`)
	require.NotContains(t, text, "private-conversation-content")
	require.NotContains(t, text, "sensitive-owner")
	require.NotContains(t, text, "provider-secret-detail")
	require.NotContains(t, text, persona.SystemPrompt)
}

func newConversationRESTRouter(t *testing.T, model orchestration.Model) *gin.Engine {
	t.Helper()
	return newConversationRESTFixture(t, model).router
}

type conversationRESTFixture struct {
	router *gin.Engine
	store  *conversation.RedisStore
	client *gateway.AgentClient
	app    *orchestration.Application
	now    *time.Time
}

func newConversationRESTFixture(t *testing.T, model orchestration.Model) conversationRESTFixture {
	t.Helper()
	mini := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	store := conversation.NewRedisStore(redisClient, "test:", 72*time.Hour)
	store.SetClock(func() time.Time { return now })
	app := orchestration.NewApplication(store, nil, model)
	quotaManager, err := quota.NewRedis(redisClient, "test:quota:", 20)
	require.NoError(t, err)

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	srv := agent.NewServer(nil, store, nil, nil).WithConversationApplication(app)
	agentv1.RegisterAgentServiceServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///conversation-bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := gateway.NewAgentClient(conn)
	router := gin.New()
	(&gateway.Handlers{
		Agent: client,
		Quota: quotaManager,
		Now:   func() time.Time { return now },
		AuthenticateSession: func(_ context.Context, token string) (authn.User, error) {
			return authn.User{ID: strings.TrimPrefix(token, "token-"), IsAdmin: token == "token-admin"}, nil
		},
	}).RegisterRoutes(router)
	return conversationRESTFixture{router: router, store: store, client: client, app: app, now: &now}
}

func authenticatedRequest(router *gin.Engine, method, path, owner, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "sos_session", Value: "token-" + owner})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
