package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

var beginBatchScript = `
local existing = redis.call('HGET', KEYS[3], ARGV[1])
local requestLedger = KEYS[3]
if not existing then
  existing = redis.call('HGET', KEYS[4], ARGV[1])
  requestLedger = KEYS[4]
end
if existing then
  local snapshot = cjson.decode(existing)
  if snapshot['Status'] == 'generating' and redis.call('EXISTS', KEYS[5]) == 0 then
    local messages = snapshot['CharacterMessages'] or {}
    if #messages == 0 then snapshot['Status'] = 'failed' else snapshot['Status'] = 'partial' end
    snapshot['InterruptionCode'] = 'generation_interrupted'
  end
  local messages = snapshot['CharacterMessages'] or {}
  if #messages == 0 then snapshot['CharacterMessages'] = nil end
  local planned = snapshot['PlannedSpeakerIDs'] or {}
  if #planned == 0 then snapshot['PlannedSpeakerIDs'] = nil end
  existing = cjson.encode(snapshot)
  redis.call('HSET', requestLedger, ARGV[1], existing)
  return {'existing', existing, ''}
end
if redis.call('EXISTS', KEYS[5]) == 1 then return {'busy', '', ''} end
redis.call('SET', KEYS[5], ARGV[3], 'PX', ARGV[5])
local sequence = redis.call('INCR', KEYS[2])
local batch = cjson.decode(ARGV[2])
batch['UserMessage']['Sequence'] = sequence
local messages = batch['CharacterMessages'] or {}
if #messages == 0 then batch['CharacterMessages'] = nil end
local planned = batch['PlannedSpeakerIDs'] or {}
if #planned == 0 then batch['PlannedSpeakerIDs'] = nil end
local user = cjson.encode(batch['UserMessage'])
local encoded = cjson.encode(batch)
redis.call('RPUSH', KEYS[1], user)
redis.call('SET', KEYS[6], encoded, 'PX', ARGV[4])
redis.call('HSET', KEYS[3], ARGV[1], encoded)
redis.call('SADD', KEYS[7], ARGV[6])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[4])
redis.call('PEXPIRE', KEYS[3], ARGV[4])
redis.call('PEXPIRE', KEYS[7], ARGV[4])
return {'started', encoded, ARGV[3]}
`

func (s *RedisStore) BeginBatch(ctx context.Context, scope Scope, clientRequestID, content string) (Batch, BeginState, string, error) {
	if err := s.migrateLegacyScope(ctx, scope, BeijingDate(s.now())); err != nil {
		return Batch{}, "", "", err
	}
	now := s.now().In(beijingLocation)
	batchID := "batch-" + newTurnID()
	user := Turn{
		TurnID: "turn-" + newTurnID(), Role: RoleUser, Content: content, CreatedAt: now,
		ConversationID: scope.ConversationID, SpeakerKind: SpeakerUser, SpeakerID: SpeakerUser,
		BatchID: batchID, DisplayName: "你",
	}
	batch := Batch{
		BatchID: batchID, ClientRequestID: clientRequestID, ConversationID: scope.ConversationID,
		PlannedSpeakerIDs: []string{}, UserMessage: user, CharacterMessages: []Turn{},
		Status: BatchGenerating, CreatedAt: now, UpdatedAt: now,
	}
	batchData, err := json.Marshal(batch)
	if err != nil {
		return Batch{}, "", "", err
	}
	date := BeijingDate(now)
	previousDate := date.AddDate(0, 0, -1)
	leaseToken := newTurnID()
	values, err := s.client.Eval(ctx, beginBatchScript, []string{
		s.messagesKey(scope, date), s.sequenceKey(scope, date), s.requestsKey(scope, date), s.requestsKey(scope, previousDate),
		s.leaseKey(scope), s.batchKey(scope, batchID), s.activeScopesKey(date),
	}, clientRequestID, batchData, leaseToken, s.ttl.Milliseconds(), s.leaseTTL.Milliseconds(), s.activeScopeMember(scope)).StringSlice()
	if err != nil {
		return Batch{}, "", "", err
	}
	if len(values) != 3 {
		return Batch{}, "", "", fmt.Errorf("invalid begin batch result")
	}
	if values[0] == "busy" {
		return Batch{}, "", "", ErrConversationBusy
	}
	if err := json.Unmarshal([]byte(values[1]), &batch); err != nil {
		return Batch{}, "", "", err
	}
	normalizeBatchCollections(&batch)
	if values[0] == "existing" {
		return batch, BeginExisting, "", nil
	}
	return batch, BeginStarted, values[2], nil
}

var appendCharacterScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return {'lease_lost', '', ''} end
local sequence = redis.call('INCR', KEYS[2])
local message = cjson.decode(ARGV[2])
message['Sequence'] = sequence
local batch = cjson.decode(ARGV[3])
local messages = batch['CharacterMessages'] or {}
messages[#messages]['Sequence'] = sequence
batch['CharacterMessages'] = messages
local encodedMessage = cjson.encode(message)
local encodedBatch = cjson.encode(batch)
redis.call('RPUSH', KEYS[3], encodedMessage)
redis.call('SET', KEYS[4], encodedBatch, 'PX', ARGV[5])
redis.call('HSET', KEYS[5], ARGV[4], encodedBatch)
redis.call('PEXPIRE', KEYS[3], ARGV[5])
redis.call('PEXPIRE', KEYS[5], ARGV[5])
redis.call('PEXPIRE', KEYS[1], ARGV[6])
return {'ok', encodedMessage, encodedBatch}
`

func (s *RedisStore) AppendCharacter(ctx context.Context, scope Scope, batch *Batch, leaseToken, speakerID, displayName, avatarURL, content string) (Turn, error) {
	now := s.now().In(beijingLocation)
	date := BeijingDate(batch.CreatedAt)
	message := Turn{
		TurnID: "turn-" + newTurnID(), Role: RoleAssistant, Content: content, CreatedAt: now,
		ConversationID: scope.ConversationID, SpeakerKind: SpeakerCharacter, SpeakerID: speakerID,
		BatchID: batch.BatchID, DisplayName: displayName, AvatarURL: avatarURL,
	}
	batch.CharacterMessages = append(batch.CharacterMessages, message)
	batch.UpdatedAt = now
	messageData, err := json.Marshal(message)
	if err != nil {
		return Turn{}, err
	}
	batchData, err := json.Marshal(batch)
	if err != nil {
		return Turn{}, err
	}
	values, err := s.client.Eval(ctx, appendCharacterScript, []string{
		s.leaseKey(scope), s.sequenceKey(scope, date), s.messagesKey(scope, date),
		s.batchKey(scope, batch.BatchID), s.requestsKey(scope, BeijingDate(batch.CreatedAt)),
	}, leaseToken, messageData, batchData, batch.ClientRequestID, s.ttl.Milliseconds(), s.leaseTTL.Milliseconds()).StringSlice()
	if err != nil {
		return Turn{}, err
	}
	if len(values) != 3 || values[0] == "lease_lost" {
		return Turn{}, ErrLeaseLost
	}
	if err := json.Unmarshal([]byte(values[1]), &message); err != nil {
		return Turn{}, err
	}
	if err := json.Unmarshal([]byte(values[2]), batch); err != nil {
		return Turn{}, err
	}
	return message, nil
}

var saveBatchScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[4])
redis.call('HSET', KEYS[3], ARGV[3], ARGV[2])
redis.call('PEXPIRE', KEYS[3], ARGV[4])
if ARGV[5] == '1' then redis.call('DEL', KEYS[1]) else redis.call('PEXPIRE', KEYS[1], ARGV[6]) end
return 1
`

func (s *RedisStore) SaveBatch(ctx context.Context, scope Scope, batch *Batch, leaseToken string, release bool) error {
	batch.UpdatedAt = s.now().In(beijingLocation)
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	releaseValue := "0"
	if release {
		releaseValue = "1"
	}
	result, err := s.client.Eval(ctx, saveBatchScript, []string{
		s.leaseKey(scope), s.batchKey(scope, batch.BatchID), s.requestsKey(scope, BeijingDate(batch.CreatedAt)),
	}, leaseToken, data, batch.ClientRequestID, s.ttl.Milliseconds(), releaseValue, s.leaseTTL.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrLeaseLost
	}
	return nil
}

var interruptBatchScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return {'lease_lost', ''} end
local encoded = redis.call('GET', KEYS[2])
if not encoded then encoded = redis.call('HGET', KEYS[3], ARGV[2]) end
if not encoded then return {'missing', ''} end
local batch = cjson.decode(encoded)
if batch['Status'] == 'generating' then
  local messages = batch['CharacterMessages'] or {}
  if #messages == 0 then
    batch['Status'] = 'failed'
    batch['CharacterMessages'] = nil
  else
    batch['Status'] = 'partial'
  end
  local planned = batch['PlannedSpeakerIDs'] or {}
  if #planned == 0 then batch['PlannedSpeakerIDs'] = nil end
  batch['InterruptionCode'] = 'generation_interrupted'
  batch['UpdatedAt'] = ARGV[4]
  encoded = cjson.encode(batch)
  redis.call('SET', KEYS[2], encoded, 'PX', ARGV[3])
  redis.call('HSET', KEYS[3], ARGV[2], encoded)
  redis.call('PEXPIRE', KEYS[3], ARGV[3])
end
redis.call('DEL', KEYS[1])
return {'ok', encoded}
`

func (s *RedisStore) InterruptBatch(ctx context.Context, scope Scope, batchID, clientRequestID, leaseToken string, createdAt time.Time) (Batch, error) {
	now := s.now().In(beijingLocation)
	values, err := s.client.Eval(ctx, interruptBatchScript, []string{
		s.leaseKey(scope), s.batchKey(scope, batchID), s.requestsKey(scope, BeijingDate(createdAt)),
	}, leaseToken, clientRequestID, s.ttl.Milliseconds(), now.Format(time.RFC3339Nano)).StringSlice()
	if err != nil {
		return Batch{}, err
	}
	if len(values) != 2 || values[0] != "ok" {
		return Batch{}, ErrLeaseLost
	}
	var batch Batch
	if err := json.Unmarshal([]byte(values[1]), &batch); err != nil {
		return Batch{}, err
	}
	normalizeBatchCollections(&batch)
	return batch, nil
}

func normalizeBatchCollections(batch *Batch) {
	if batch.PlannedSpeakerIDs == nil {
		batch.PlannedSpeakerIDs = []string{}
	}
	if batch.CharacterMessages == nil {
		batch.CharacterMessages = []Turn{}
	}
}

func (s *RedisStore) Messages(ctx context.Context, scope Scope) ([]Turn, error) {
	return s.MessagesForDateScope(ctx, scope, s.now())
}

func (s *RedisStore) MessagesForDateScope(ctx context.Context, scope Scope, day time.Time) ([]Turn, error) {
	date := BeijingDate(day)
	if err := s.migrateLegacyScope(ctx, scope, date); err != nil {
		return nil, err
	}
	values, err := s.client.LRange(ctx, s.messagesKey(scope, date), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	messages := make([]Turn, 0, len(values))
	legacyBatch := ""
	for i, value := range values {
		var message Turn
		if err := json.Unmarshal([]byte(value), &message); err != nil {
			return nil, err
		}
		if message.ConversationID == "" {
			message.ConversationID = scope.ConversationID
		}
		message.Sequence = uint64(i + 1)
		if message.Role == RoleUser {
			message.SpeakerKind, message.SpeakerID, message.DisplayName = SpeakerUser, SpeakerUser, "你"
			if message.BatchID == "" {
				legacyBatch = "legacy-batch-" + message.TurnID
				message.BatchID = legacyBatch
			}
		} else if message.SpeakerID == "" {
			message.SpeakerKind, message.SpeakerID, message.DisplayName = SpeakerCharacter, "haruhi", "凉宫春日"
			message.AvatarURL = "/app/assets/avatars/haruhi.svg"
			if message.BatchID == "" {
				if legacyBatch == "" {
					legacyBatch = "legacy-batch-" + message.TurnID
				}
				message.BatchID = legacyBatch
			}
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (s *RedisStore) ClearScopeToday(ctx context.Context, scope Scope) error {
	busy, err := s.client.Exists(ctx, s.leaseKey(scope)).Result()
	if err != nil {
		return err
	}
	if busy > 0 {
		return ErrConversationBusy
	}
	return s.ClearScopeDate(ctx, scope, s.now())
}

func (s *RedisStore) ClearScopeDate(ctx context.Context, scope Scope, day time.Time) error {
	date := BeijingDate(day)
	if err := s.migrateLegacyScope(ctx, scope, date); err != nil {
		return err
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, s.messagesKey(scope, date), s.sequenceKey(scope, date))
	// Request results outlive visible history so a retried client identity cannot
	// regenerate model output or bypass the user-level quota after a clear.
	pipe.SRem(ctx, s.activeScopesKey(date), s.activeScopeMember(scope))
	if scope.ConversationID == DefaultConversationID {
		pipe.SRem(ctx, s.activeKey(date), s.activeMember(scope.Identity))
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisStore) ActiveScopes(ctx context.Context, day time.Time) ([]Scope, error) {
	date := BeijingDate(day)
	members, err := s.client.SMembers(ctx, s.activeScopesKey(date)).Result()
	if err != nil {
		return nil, err
	}
	legacy, err := s.ActiveIdentities(ctx, date)
	if err != nil {
		return nil, err
	}
	for _, identity := range legacy {
		members = append(members, s.activeScopeMember(Scope{Identity: identity, ConversationID: DefaultConversationID}))
	}
	sort.Strings(members)
	seen := make(map[string]struct{}, len(members))
	out := make([]Scope, 0, len(members))
	for _, member := range members {
		if _, duplicate := seen[member]; duplicate {
			continue
		}
		seen[member] = struct{}{}
		parts := strings.SplitN(member, "|", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			continue
		}
		out = append(out, Scope{Identity: Identity{Channel: parts[0], ExternalID: parts[1]}, ConversationID: parts[2]})
	}
	return out, nil
}

var migrateLegacyScript = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  return redis.call('LLEN', KEYS[2])
end
local legacy = redis.call('LRANGE', KEYS[1], 0, -1)
local current = redis.call('LRANGE', KEYS[2], 0, -1)
redis.call('DEL', KEYS[2])
for _, value in ipairs(legacy) do redis.call('RPUSH', KEYS[2], value) end
for _, value in ipairs(current) do redis.call('RPUSH', KEYS[2], value) end
redis.call('DEL', KEYS[1])
local total = #legacy + #current
redis.call('SET', KEYS[3], total)
redis.call('PEXPIRE', KEYS[2], ARGV[1])
redis.call('PEXPIRE', KEYS[3], ARGV[1])
return total
`

func (s *RedisStore) migrateLegacyScope(ctx context.Context, scope Scope, date time.Time) error {
	if scope.ConversationID != DefaultConversationID {
		return nil
	}
	return s.client.Eval(ctx, migrateLegacyScript, []string{
		s.turnsKey(scope.Identity, date), s.messagesKey(scope, date), s.sequenceKey(scope, date),
	}, s.ttl.Milliseconds()).Err()
}

func (s *RedisStore) scopeTag(scope Scope) string {
	return fmt.Sprintf("{%s|%s|%s}", scope.Identity.Channel, scope.Identity.ExternalID, scope.ConversationID)
}

func (s *RedisStore) messagesKey(scope Scope, date time.Time) string {
	return fmt.Sprintf("%sconversation:v2:%s:%s:messages", s.prefix, s.scopeTag(scope), date.Format("2006-01-02"))
}

func (s *RedisStore) sequenceKey(scope Scope, date time.Time) string {
	return fmt.Sprintf("%sconversation:v2:%s:%s:sequence", s.prefix, s.scopeTag(scope), date.Format("2006-01-02"))
}

func (s *RedisStore) batchKey(scope Scope, batchID string) string {
	return fmt.Sprintf("%sconversation:v2:%s:batch:%s", s.prefix, s.scopeTag(scope), batchID)
}

func (s *RedisStore) requestsKey(scope Scope, date time.Time) string {
	return fmt.Sprintf("%sconversation:v2:%s:%s:requests", s.prefix, s.scopeTag(scope), date.Format("2006-01-02"))
}

func (s *RedisStore) leaseKey(scope Scope) string {
	return fmt.Sprintf("%sconversation:v2:%s:lease", s.prefix, s.scopeTag(scope))
}

func (s *RedisStore) activeScopesKey(date time.Time) string {
	return fmt.Sprintf("%sconversation:v2:active:%s", s.prefix, date.Format("2006-01-02"))
}

func (s *RedisStore) activeScopeMember(scope Scope) string {
	return scope.Identity.Channel + "|" + scope.Identity.ExternalID + "|" + scope.ConversationID
}
