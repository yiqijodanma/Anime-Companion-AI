package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	"companion-ai/internal/chat"
	"companion-ai/internal/conversation"
	"companion-ai/internal/gateway"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
	"companion-ai/internal/testdb"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("哼，本团长在呢！", nil), nil
}

func TestEndToEndReplyThroughGRPC(t *testing.T) {
	db := testdb.Open(t)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	s := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	conv := conversation.NewRedisStore(redisClient, "test:", 72*time.Hour)
	fm := fakeModel{}
	srv := agent.NewServer(repo, conv, chat.NewReplier(fm), summarize.NewSummarizer(fm))

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(grpcServer, srv)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	client := gateway.NewAgentClient(conn)
	reply, err := client.Reply(context.Background(), "wechat", "u1", "你好")
	require.NoError(t, err)
	require.Equal(t, "哼，本团长在呢！", reply)

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Empty(t, msgs)

	clientMsgs, err := client.ListMessages(context.Background(), "wechat", "u1")
	require.NoError(t, err)
	require.Len(t, clientMsgs, 2)
	require.Equal(t, memory.RoleUser, clientMsgs[0].Role)
	require.Equal(t, "你好", clientMsgs[0].Content)
	require.NotEmpty(t, clientMsgs[0].CreatedAt)
	require.Equal(t, memory.RoleAssistant, clientMsgs[1].Role)
	require.Equal(t, "哼，本团长在呢！", clientMsgs[1].Content)
	require.NotEmpty(t, clientMsgs[1].CreatedAt)

	require.NoError(t, client.DeleteMessages(context.Background(), "wechat", "u1"))
	turns, err := conv.RecentTurns(context.Background(), conversation.Identity{Channel: "wechat", ExternalID: "u1"}, 0)
	require.NoError(t, err)
	require.Empty(t, turns)
}
