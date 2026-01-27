package models

import (
	"time"

	"github.com/google/uuid"
)

type FriendRequestStatus string

const (
	FriendRequestPending  FriendRequestStatus = "pending"
	FriendRequestAccepted FriendRequestStatus = "accepted"
	FriendRequestDeclined FriendRequestStatus = "declined"
)

type FriendRequest struct {
	ID         uuid.UUID           `json:"id" db:"id"`
	FromUserID uuid.UUID           `json:"from_user_id" db:"from_user_id"`
	ToUserID   uuid.UUID           `json:"to_user_id" db:"to_user_id"`
	Status     FriendRequestStatus `json:"status" db:"status"`
	CreatedAt  time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at" db:"updated_at"`
}

type Block struct {
	ID        uuid.UUID `json:"id" db:"id"`
	BlockerID uuid.UUID `json:"blocker_id" db:"blocker_id"`
	BlockedID uuid.UUID `json:"blocked_id" db:"blocked_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// API responses with user data included
type FriendRequestWithUser struct {
	ID        uuid.UUID           `json:"id"`
	Status    FriendRequestStatus `json:"status"`
	User      *User               `json:"user"` // The other user (from or to depending on context)
	CreatedAt time.Time           `json:"created_at"`
}

type FriendWithUser struct {
	FriendshipID uuid.UUID `json:"friendship_id"`
	User         *User     `json:"user"`
	Since        time.Time `json:"since"` // When friendship was accepted
}

type BlockWithUser struct {
	ID        uuid.UUID `json:"id"`
	User      *User     `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

// Request DTOs
type SendFriendRequestDTO struct {
	UserID string `json:"user_id" validate:"required,uuid"`
}

type SendFriendRequestByUsernameDTO struct {
	Username string `json:"username" validate:"required,min=3,max=32"`
}

type BlockUserDTO struct {
	UserID string `json:"user_id" validate:"required,uuid"`
}
