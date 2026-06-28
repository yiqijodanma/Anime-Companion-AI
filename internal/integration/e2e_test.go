package integration

import (
	"context"
	"net"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	"companion-ai/internal/chat"
	"companion-ai/internal/gateway"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("哼，本团长在呢！", nil), nil
}

func TestEndToEndReplyThroughGRPC(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

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
}
