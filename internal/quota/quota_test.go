package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"companion-ai/internal/testredis"
)

func newTestManager(t *testing.T, limit int) *Redis {
	t.Helper()
	client := testredis.Open(t, 1)
	manager, err := NewRedis(client, "test:quota:", limit)
	require.NoError(t, err)
	return manager
}

func TestSnapshotJSONContract(t *testing.T) {
	reset := time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	limited, err := json.Marshal(Snapshot{Limit: 20, Used: 3, Remaining: 17, ResetAt: reset, Revision: 7})
	require.NoError(t, err)
	require.JSONEq(t, `{"unlimited":false,"limit":20,"used":3,"remaining":17,"reset_at":"2026-07-24T00:00:00+08:00","revision":7}`, string(limited))

	unlimited, err := json.Marshal(Snapshot{Unlimited: true, Limit: 20, Used: 19, Remaining: 1, ResetAt: reset})
	require.NoError(t, err)
	require.Equal(t, `{"unlimited":true}`, string(unlimited))
}

func TestQuotaRevisionAdvancesOnlyWhenLedgerChanges(t *testing.T) {
	manager := newTestManager(t, 2)
	ctx := context.Background()
	subject := Subject{UserID: "revision-user"}
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	reservation, snapshot, err := manager.Reserve(ctx, subject, "request-1", now)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Revision)

	_, duplicate, err := manager.Reserve(ctx, subject, "request-1", now)
	require.NoError(t, err)
	require.Equal(t, snapshot.Revision, duplicate.Revision)

	pending, err := manager.Settle(ctx, reservation, OutcomePending, now)
	require.NoError(t, err)
	require.Equal(t, snapshot.Revision, pending.Revision)

	released, err := manager.Settle(ctx, reservation, OutcomeFailed, now)
	require.NoError(t, err)
	require.EqualValues(t, 2, released.Revision)

	retry, reservedAgain, err := manager.Reserve(ctx, subject, "request-1", now)
	require.NoError(t, err)
	require.EqualValues(t, 3, reservedAgain.Revision)

	delivered, err := manager.Settle(ctx, retry, OutcomeDelivered, now)
	require.NoError(t, err)
	require.EqualValues(t, 4, delivered.Revision)
}

func TestRedisReservationSettlementAndRetry(t *testing.T) {
	manager := newTestManager(t, 2)
	ctx := context.Background()
	subject := Subject{UserID: "user-1"}
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	first, snapshot, err := manager.Reserve(ctx, subject, "request-1", now)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Used)
	require.Equal(t, 1, snapshot.Remaining)

	duplicate, snapshot, err := manager.Reserve(ctx, subject, "request-1", now)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Used)
	require.Equal(t, first.requestHash, duplicate.requestHash)

	snapshot, err = manager.Settle(ctx, first, OutcomeDelivered, now)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Used)

	_, snapshot, err = manager.Reserve(ctx, subject, "request-1", now)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Used, "a delivered retry must not reserve again")

	second, snapshot, err := manager.Reserve(ctx, subject, "request-2", now)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.Used)

	_, exhausted, err := manager.Reserve(ctx, subject, "request-3", now)
	require.ErrorIs(t, err, ErrExhausted)
	require.Equal(t, 0, exhausted.Remaining)

	snapshot, err = manager.Settle(ctx, second, OutcomeFailed, now)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Used)
	require.Equal(t, 1, snapshot.Remaining)

	retry, snapshot, err := manager.Reserve(ctx, subject, "request-2", now)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.Used, "a definitely failed request may be retried")
	snapshot, err = manager.Settle(ctx, retry, OutcomeDelivered, now)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.Used)

	snapshot, err = manager.Settle(ctx, retry, OutcomeFailed, now)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.Used, "a delivered request can never be released")
}

func TestPendingReservationKeepsCapacityUntilRetryResolves(t *testing.T) {
	manager := newTestManager(t, 1)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	subject := Subject{UserID: "user-pending"}

	reservation, _, err := manager.Reserve(ctx, subject, "request-pending", now)
	require.NoError(t, err)
	snapshot, err := manager.Settle(ctx, reservation, OutcomePending, now)
	require.NoError(t, err)
	require.Zero(t, snapshot.Remaining)

	_, _, err = manager.Reserve(ctx, subject, "another-request", now)
	require.ErrorIs(t, err, ErrExhausted)

	duplicate, _, err := manager.Reserve(ctx, subject, "request-pending", now)
	require.NoError(t, err)
	_, err = manager.Settle(ctx, duplicate, OutcomeFailed, now)
	require.NoError(t, err)
	_, snapshot, err = manager.Reserve(ctx, subject, "another-request", now)
	require.NoError(t, err)
	require.Zero(t, snapshot.Remaining)
}

func TestConcurrentReservationsRespectCeiling(t *testing.T) {
	manager := newTestManager(t, 20)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	subject := Subject{UserID: "parallel-user"}
	var accepted atomic.Int32
	var exhausted atomic.Int32
	var unexpected atomic.Value
	var wait sync.WaitGroup

	for i := 0; i < 64; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, _, err := manager.Reserve(ctx, subject, fmt.Sprintf("request-%d", index), now)
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrExhausted):
				exhausted.Add(1)
			default:
				unexpected.Store(err)
			}
		}(i)
	}
	wait.Wait()
	require.Nil(t, unexpected.Load())
	require.EqualValues(t, 20, accepted.Load())
	require.EqualValues(t, 44, exhausted.Load())
}

func TestQuotaDayResetsAtBeijingMidnight(t *testing.T) {
	manager := newTestManager(t, 20)
	ctx := context.Background()
	subject := Subject{UserID: "midnight-user"}
	before := time.Date(2026, 7, 23, 15, 59, 0, 0, time.UTC)
	reservation, snapshot, err := manager.Reserve(ctx, subject, "request-before", before)
	require.NoError(t, err)
	require.Equal(t, "2026-07-24T00:00:00+08:00", snapshot.ResetAt.Format(time.RFC3339))
	_, err = manager.Settle(ctx, reservation, OutcomeDelivered, before)
	require.NoError(t, err)

	after := time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC)
	snapshot, err = manager.Snapshot(ctx, subject, after)
	require.NoError(t, err)
	require.Zero(t, snapshot.Used)
	require.Equal(t, 20, snapshot.Remaining)
	require.Equal(t, "2026-07-25T00:00:00+08:00", snapshot.ResetAt.Format(time.RFC3339))
}

func TestSettlementCrossingMidnightReturnsCurrentDaySnapshot(t *testing.T) {
	manager := newTestManager(t, 20)
	ctx := context.Background()
	subject := Subject{UserID: "long-request-user"}
	before := time.Date(2026, 7, 23, 15, 59, 50, 0, time.UTC)
	reservation, _, err := manager.Reserve(ctx, subject, "request-crossing-midnight", before)
	require.NoError(t, err)

	after := time.Date(2026, 7, 23, 16, 0, 10, 0, time.UTC)
	snapshot, err := manager.Settle(ctx, reservation, OutcomeDelivered, after)
	require.NoError(t, err)
	require.Zero(t, snapshot.Used)
	require.Equal(t, 20, snapshot.Remaining)
	require.Equal(t, "2026-07-25T00:00:00+08:00", snapshot.ResetAt.Format(time.RFC3339))

	oldDay, err := manager.Snapshot(ctx, subject, before)
	require.NoError(t, err)
	require.Equal(t, 1, oldDay.Used, "the delivered request remains committed to the day where it was reserved")
}

func TestSameRequestRetryUsesImmediatelyPreviousQuotaDayLedger(t *testing.T) {
	manager := newTestManager(t, 20)
	ctx := context.Background()
	subject := Subject{UserID: "midnight-retry-ledger"}
	before := time.Date(2026, 7, 23, 15, 59, 50, 0, time.UTC)
	first, _, err := manager.Reserve(ctx, subject, "scoped-request", before)
	require.NoError(t, err)
	_, err = manager.Settle(ctx, first, OutcomeDelivered, before)
	require.NoError(t, err)

	after := time.Date(2026, 7, 23, 16, 0, 10, 0, time.UTC)
	retry, snapshot, err := manager.Reserve(ctx, subject, "scoped-request", after)
	require.NoError(t, err)
	require.Equal(t, first.key, retry.key)
	require.Zero(t, snapshot.Used)
	require.Equal(t, 20, snapshot.Remaining)
	require.Equal(t, "2026-07-25T00:00:00+08:00", snapshot.ResetAt.Format(time.RFC3339))

	snapshot, err = manager.Settle(ctx, retry, OutcomeDelivered, after)
	require.NoError(t, err)
	require.Zero(t, snapshot.Used)
	require.Equal(t, 20, snapshot.Remaining)
}

func TestAdministratorBypassesRedis(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: time.Millisecond})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	manager, err := NewRedis(client, "test:quota:", 20)
	require.NoError(t, err)
	subject := Subject{UserID: "admin", IsAdmin: true}

	reservation, snapshot, err := manager.Reserve(context.Background(), subject, "admin-request", time.Now())
	require.NoError(t, err)
	require.True(t, snapshot.Unlimited)
	snapshot, err = manager.Settle(context.Background(), reservation, OutcomeDelivered, time.Now())
	require.NoError(t, err)
	require.True(t, snapshot.Unlimited)
}

func TestNewRedisRejectsInvalidDependencies(t *testing.T) {
	_, err := NewRedis(nil, "quota:", 20)
	require.Error(t, err)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	_, err = NewRedis(client, "", 20)
	require.Error(t, err)
	_, err = NewRedis(client, "quota:", 0)
	require.Error(t, err)
}
