package models

import "github.com/google/uuid"

// ActiveCallInfo represents an active call in a conversation
type ActiveCallInfo struct {
	CallID         uuid.UUID   `json:"call_id"`
	ConversationID uuid.UUID   `json:"conversation_id"`
	Participants   []uuid.UUID `json:"participants"`
	StartedAt      string      `json:"started_at"`
}

// ReadyEvent is sent when client connects with all initial data
type ReadyEvent struct {
	User             *User                      `json:"user"`
	Friends          []*FriendWithUser          `json:"friends"`
	IncomingRequests []*FriendRequestWithUser   `json:"incoming_requests"`
	OutgoingRequests []*FriendRequestWithUser   `json:"outgoing_requests"`
	Conversations    []*ConversationWithDetails `json:"conversations"`
	ActiveCalls      []*ActiveCallInfo          `json:"active_calls"`
}

// Friend events
type FriendRequestCreateEvent struct {
	Request *FriendRequestWithUser `json:"request"`
}

type FriendRequestDeleteEvent struct {
	RequestID uuid.UUID `json:"request_id"`
	UserID    uuid.UUID `json:"user_id"` // The other user involved
}

type RelationshipAddEvent struct {
	Friend *FriendWithUser `json:"friend"`
}

type RelationshipRemoveEvent struct {
	UserID uuid.UUID `json:"user_id"`
}

// Message events
type MessageCreateEvent struct {
	Message        *Message  `json:"message"`
	ConversationID uuid.UUID `json:"conversation_id"`
}

type MessageDeleteEvent struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
}

// Reaction events
type ReactionAddEvent struct {
	Reaction       *Reaction `json:"reaction"`
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
}

type ReactionRemoveEvent struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
	Emoji          string    `json:"emoji"`
}

// Presence events (for future)
type PresenceUpdateEvent struct {
	UserID uuid.UUID `json:"user_id"`
	Status string    `json:"status"`
}

// Call events - single event for all call state changes
type CallStateEvent struct {
	ConversationID uuid.UUID   `json:"conversation_id"`
	CallID         *uuid.UUID  `json:"call_id"`         // nil = no active call
	Participants   []uuid.UUID `json:"participants"`    // who is currently in the call
}
