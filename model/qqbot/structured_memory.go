package qqbot

import (
	"context"
	"time"
)

type MemoryKind string

const (
	MemoryKindFact        MemoryKind = "fact"
	MemoryKindPreference  MemoryKind = "preference"
	MemoryKindEpisode     MemoryKind = "episode"
	MemoryKindInstruction MemoryKind = "instruction"
	MemoryKindSummary     MemoryKind = "summary"
)

type MemoryCandidateAction string

const (
	MemoryActionUpsert MemoryCandidateAction = "upsert"
	MemoryActionForget MemoryCandidateAction = "forget"
)

type MemorySourceType string

const (
	MemorySourceExplicit MemorySourceType = "explicit"
	MemorySourceInferred MemorySourceType = "inferred"
	MemorySourceSummary  MemorySourceType = "summary"
)

type MemoryVisibility string

const (
	// MemoryVisibilitySession keeps a memory inside the QQ private chat or group
	// in which it was learned.
	MemoryVisibilitySession MemoryVisibility = "session"
	// MemoryVisibilityUser allows a non-sensitive, explicit memory about the
	// current speaker to follow that speaker across conversations.
	MemoryVisibilityUser MemoryVisibility = "user"
)

type MemoryStatus string

const (
	MemoryStatusActive     MemoryStatus = "active"
	MemoryStatusSuperseded MemoryStatus = "superseded"
	MemoryStatusForgotten  MemoryStatus = "forgotten"
)

// MemoryCandidate is proposed by the LLM memory gate. Storage still validates
// scope, confidence, versioning, and provenance before it becomes canonical.
type MemoryCandidate struct {
	Action        MemoryCandidateAction `json:"action"`
	Key           string                `json:"key"`
	Kind          MemoryKind            `json:"kind"`
	Topic         string                `json:"topic"`
	Entity        string                `json:"entity,omitempty"`
	Content       string                `json:"content,omitempty"`
	Evidence      string                `json:"evidence,omitempty"`
	SourceType    MemorySourceType      `json:"source_type"`
	Confidence    float64               `json:"confidence"`
	Importance    float64               `json:"importance"`
	Visibility    MemoryVisibility      `json:"visibility"`
	Sensitive     bool                  `json:"sensitive"`
	RetentionDays int                   `json:"retention_days,omitempty"`
}

// StructuredMemoryItem is a derived view over immutable message events.
// Superseded entries remain queryable in SQLite for audit and conflict repair.
type StructuredMemoryItem struct {
	ID              string           `json:"id"`
	ScopeKey        string           `json:"scope_key"`
	SubjectUserID   string           `json:"subject_user_id,omitempty"`
	SubjectName     string           `json:"subject_name,omitempty"`
	Key             string           `json:"key"`
	Kind            MemoryKind       `json:"kind"`
	Topic           string           `json:"topic"`
	Entity          string           `json:"entity,omitempty"`
	Content         string           `json:"content"`
	Evidence        string           `json:"evidence,omitempty"`
	SourceType      MemorySourceType `json:"source_type"`
	SourceSession   string           `json:"source_session"`
	SourceGroupID   string           `json:"source_group_id,omitempty"`
	SourceMessageID string           `json:"source_message_id,omitempty"`
	SourceEventTime time.Time        `json:"source_event_time,omitempty"`
	Confidence      float64          `json:"confidence"`
	Importance      float64          `json:"importance"`
	Visibility      MemoryVisibility `json:"visibility"`
	Sensitive       bool             `json:"sensitive"`
	ExpiresAt       time.Time        `json:"expires_at,omitempty"`
	LastVerifiedAt  time.Time        `json:"last_verified_at,omitempty"`
	Version         int              `json:"version"`
	SupersedesID    string           `json:"supersedes_id,omitempty"`
	Status          MemoryStatus     `json:"status"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	RetrievalScore  float64          `json:"retrieval_score,omitempty"`
}

type MemoryWriteRequest struct {
	SubjectUserID   string
	SubjectName     string
	Session         string
	EventKind       EventKind
	GroupID         string
	SourceMessageID string
	SourceEventTime time.Time
	Candidates      []MemoryCandidate
}

type StructuredMemoryQuery struct {
	SubjectUserID string
	Session       string
	GroupID       string
	Text          string
	Now           time.Time
	MaxCandidates int
	Kinds         []MemoryKind
}

type MemoryJobKind string

const (
	MemoryJobEvent   MemoryJobKind = "event"
	MemoryJobSummary MemoryJobKind = "summary"
)

type MemoryJobPayload struct {
	Kind    MemoryJobKind  `json:"kind"`
	Session string         `json:"session"`
	Event   MessageEvent   `json:"event,omitempty"`
	Events  []MessageEvent `json:"events,omitempty"`
}

type MemoryJob struct {
	ID       string
	Payload  MemoryJobPayload
	Attempts int
}

// StructuredMemoryStore keeps extraction work durable and stores the derived
// memory view separately from user relationship profiles.
type StructuredMemoryStore interface {
	EnqueueMemoryJob(ctx context.Context, payload MemoryJobPayload) (id string, inserted bool, err error)
	ClaimNextMemoryJob(ctx context.Context, leaseOwner string, leaseUntil time.Time) (MemoryJob, bool, error)
	CompleteMemoryJob(ctx context.Context, id string, leaseOwner string) error
	RetryMemoryJob(ctx context.Context, id string, leaseOwner string, availableAt time.Time, lastError string) error
	ReleaseMemoryJobLeases(ctx context.Context, leaseOwner string) error
	ApplyMemoryCandidates(ctx context.Context, request MemoryWriteRequest) ([]StructuredMemoryItem, error)
	ListStructuredMemories(ctx context.Context, query StructuredMemoryQuery) ([]StructuredMemoryItem, error)
}
