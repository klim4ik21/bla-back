package models

import (
	"time"

	"github.com/google/uuid"
)

type StickerPack struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	Name        string     `json:"name" db:"name"`
	Description string     `json:"description" db:"description"`
	CoverURL    string     `json:"cover_url" db:"cover_url"`
	IsOfficial  bool       `json:"is_official" db:"is_official"`
	CreatorID   *uuid.UUID `json:"creator_id" db:"creator_id"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`

	// Joined fields
	Stickers []*Sticker `json:"stickers,omitempty"`
	Creator  *User      `json:"creator,omitempty"`
}

type Sticker struct {
	ID        uuid.UUID `json:"id" db:"id"`
	PackID    uuid.UUID `json:"pack_id" db:"pack_id"`
	Emoji     string    `json:"emoji" db:"emoji"` // Associated emoji
	FileURL   string    `json:"file_url" db:"file_url"`
	FileType  string    `json:"file_type" db:"file_type"` // "tgs", "webp", "png"
	Width     int       `json:"width" db:"width"`
	Height    int       `json:"height" db:"height"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// User's saved sticker packs
type UserStickerPack struct {
	UserID    uuid.UUID `db:"user_id"`
	PackID    uuid.UUID `db:"pack_id"`
	AddedAt   time.Time `db:"added_at"`
	SortOrder int       `db:"sort_order"`
}

// Request DTOs
type CreateStickerPackRequest struct {
	Name        string `json:"name" validate:"required,max=64"`
	Description string `json:"description" validate:"max=256"`
}

type AddStickerRequest struct {
	Emoji string `json:"emoji" validate:"required,max=32"`
}
