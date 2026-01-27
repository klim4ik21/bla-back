package models

import (
	"time"

	"github.com/google/uuid"
)

type Message struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	ConversationID uuid.UUID  `json:"conversation_id" db:"conversation_id"`
	SenderID       uuid.UUID  `json:"sender_id" db:"sender_id"`
	Content        string     `json:"content" db:"content"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`

	// Joined fields
	Sender      *User         `json:"sender,omitempty"`
	Attachments []*Attachment `json:"attachments,omitempty"`
	Reactions   []*Reaction   `json:"reactions,omitempty"`
}

type Reaction struct {
	ID        uuid.UUID `json:"id" db:"id"`
	MessageID uuid.UUID `json:"message_id" db:"message_id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	Emoji     string    `json:"emoji" db:"emoji"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`

	// Joined fields
	User *User `json:"user,omitempty"`
}

type ReactionGroup struct {
	Emoji string  `json:"emoji"`
	Count int     `json:"count"`
	Users []*User `json:"users"`
}

type Attachment struct {
	ID        uuid.UUID `json:"id" db:"id"`
	MessageID uuid.UUID `json:"message_id" db:"message_id"`
	Type      string    `json:"type" db:"type"`           // "image", "file", etc.
	URL       string    `json:"url" db:"url"`
	Filename  string    `json:"filename" db:"filename"`
	Size      int64     `json:"size" db:"size"`
	Width     *int      `json:"width,omitempty" db:"width"`
	Height    *int      `json:"height,omitempty" db:"height"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Conversation struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	Type      string     `json:"type" db:"type"` // "dm" or "group"
	Name      *string    `json:"name" db:"name"` // for groups
	AvatarURL *string    `json:"avatar_url" db:"avatar_url"`
	OwnerID   *uuid.UUID `json:"owner_id" db:"owner_id"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`

	// Joined fields
	Participants []*User   `json:"participants,omitempty"`
	LastMessage  *Message  `json:"last_message,omitempty"`
}

type ConversationParticipant struct {
	ConversationID uuid.UUID `db:"conversation_id"`
	UserID         uuid.UUID `db:"user_id"`
	JoinedAt       time.Time `db:"joined_at"`
}

// Request/Response DTOs
type SendMessageRequest struct {
	Content       string   `json:"content" validate:"max=4000"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
}

type CreateDMRequest struct {
	UserID string `json:"user_id" validate:"required,uuid"`
}

type CreateGroupRequest struct {
	Name           string   `json:"name" validate:"max=100"`
	ParticipantIDs []string `json:"participant_ids" validate:"required,min=1"`
}

type AddParticipantsRequest struct {
	UserIDs []string `json:"user_ids" validate:"required,min=1"`
}

type UpdateGroupRequest struct {
	Name string `json:"name" validate:"max=100"`
}

type AddReactionRequest struct {
	Emoji string `json:"emoji" validate:"required,max=32"`
}

type ConversationWithDetails struct {
	ID           uuid.UUID  `json:"id"`
	Type         string     `json:"type"`
	Name         *string    `json:"name"`
	AvatarURL    *string    `json:"avatar_url"`
	OwnerID      *uuid.UUID `json:"owner_id"`
	Participants []*User    `json:"participants"`
	LastMessage  *Message   `json:"last_message"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// SSE Event types
type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type NewMessageEvent struct {
	Message      *Message  `json:"message"`
	Conversation uuid.UUID `json:"conversation_id"`
}
