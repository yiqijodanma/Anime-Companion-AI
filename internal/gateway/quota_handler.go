package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	authn "companion-ai/internal/auth"
	"companion-ai/internal/conversation"
	"companion-ai/internal/quota"
)

var errQuotaManagerUnavailable = errors.New("quota manager is unavailable")

func (h *Handlers) quotaNow() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func quotaSubject(user authn.User) quota.Subject {
	return quota.Subject{UserID: user.ID, IsAdmin: user.IsAdmin}
}

func scopedQuotaRequestID(conversationID, clientRequestID string) string {
	return conversationID + "\x00" + clientRequestID
}

func (h *Handlers) currentQuota(ctx context.Context, user authn.User) (quota.Snapshot, error) {
	if h.Quota == nil {
		if user.IsAdmin {
			return quota.Snapshot{Unlimited: true}, nil
		}
		return quota.Snapshot{}, errQuotaManagerUnavailable
	}
	return h.Quota.Snapshot(ctx, quotaSubject(user), h.quotaNow())
}

func (h *Handlers) reserveQuota(c *gin.Context, user authn.User, requestID string) (quota.Reservation, quota.Snapshot, bool) {
	if h.Quota == nil {
		if user.IsAdmin {
			return quota.Reservation{}, quota.Snapshot{Unlimited: true}, true
		}
		log := h.Log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("daily quota rejected", "reason", "manager_unavailable")
		quotaUnavailable(c)
		return quota.Reservation{}, quota.Snapshot{}, false
	}
	reservation, snapshot, err := h.Quota.Reserve(c.Request.Context(), quotaSubject(user), requestID, h.quotaNow())
	if err == nil {
		return reservation, snapshot, true
	}
	log := h.Log
	if log == nil {
		log = slog.Default()
	}
	if errors.Is(err, quota.ErrExhausted) {
		log.Info("daily quota rejected", "reason", "exhausted")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{
				"code":     "daily_quota_exhausted",
				"message":  "今日对话额度已用完",
				"retry_at": snapshot.ResetAt.Format(time.RFC3339),
			},
			"quota": snapshot,
		})
		return quota.Reservation{}, snapshot, false
	}
	log.Warn("daily quota rejected", "reason", "unavailable", "err", err)
	quotaUnavailable(c)
	return quota.Reservation{}, quota.Snapshot{}, false
}

func (h *Handlers) settleQuota(ctx context.Context, reservation quota.Reservation, outcome quota.Outcome) (quota.Snapshot, error) {
	if h.Quota == nil {
		return quota.Snapshot{Unlimited: true}, nil
	}
	return h.Quota.Settle(ctx, reservation, outcome, h.quotaNow())
}

func (h *Handlers) settleAgentError(c *gin.Context, reservation quota.Reservation, agentErr error) bool {
	outcome := quota.OutcomePending
	if definitiveAgentFailure(agentErr) {
		outcome = quota.OutcomeFailed
	}
	if _, err := h.settleQuota(c.Request.Context(), reservation, outcome); err != nil {
		log := h.Log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("daily quota settlement failed", "outcome", string(outcome), "err", err)
		quotaUnavailable(c)
		return false
	}
	return true
}

func quotaOutcome(batch ResponseBatch) quota.Outcome {
	if len(batch.CharacterMessages) > 0 {
		return quota.OutcomeDelivered
	}
	switch batch.Status {
	case conversation.BatchFailed, conversation.BatchPartial, conversation.BatchComplete:
		return quota.OutcomeFailed
	default:
		return quota.OutcomePending
	}
}

func definitiveAgentFailure(err error) bool {
	switch grpcstatus.Code(err) {
	case codes.InvalidArgument, codes.ResourceExhausted, codes.NotFound, codes.Aborted,
		codes.AlreadyExists, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

func quotaUnavailable(c *gin.Context) {
	apiError(c, http.StatusServiceUnavailable, "quota_unavailable", "对话额度暂时不可用，请稍后重试")
}
