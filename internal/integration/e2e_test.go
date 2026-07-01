package integration

import (
	"context"
	"net"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	"companion-ai/internal/chat"
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
	fm := fakeModel{}
	srv := agent.NewServer(repo, chat.NewReplier(fm), summarize.NewSummarizer(fm))

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
	defer conn.Close()

	client := gateway.NewAgentClient(conn)
	reply, err := client.Reply(context.Background(), "u1", "你好")
	require.NoError(t, err)
	require.Equal(t, "哼，本团长在呢！", reply)

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	clientMsgs, err := client.ListMessages(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, clientMsgs, 2)
	require.Equal(t, uint64(msgs[0].ID), clientMsgs[0].ID)
	require.Equal(t, memory.RoleUser, clientMsgs[0].Role)
	require.Equal(t, "你好", clientMsgs[0].Content)
	require.NotEmpty(t, clientMsgs[0].CreatedAt)
	require.Equal(t, uint64(msgs[1].ID), clientMsgs[1].ID)
	require.Equal(t, memory.RoleAssistant, clientMsgs[1].Role)
	require.Equal(t, "哼，本团长在呢！", clientMsgs[1].Content)
	require.NotEmpty(t, clientMsgs[1].CreatedAt)

	require.NoError(t, client.DeleteMessages(context.Background(), "u1"))
	msgs, err = repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Empty(t, msgs)
}
