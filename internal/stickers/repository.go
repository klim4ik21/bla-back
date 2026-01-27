package stickers

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/bla-back/internal/models"
)

var (
	ErrPackNotFound    = errors.New("sticker pack not found")
	ErrStickerNotFound = errors.New("sticker not found")
	ErrNotOwner        = errors.New("not the pack owner")
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetAllPacks returns all available sticker packs (official + user's saved)
func (r *Repository) GetAllPacks(ctx context.Context, userID uuid.UUID) ([]*models.StickerPack, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT sp.id, sp.name, sp.description, sp.cover_url, sp.is_official, sp.creator_id, sp.created_at, sp.updated_at
		FROM sticker_packs sp
		LEFT JOIN user_sticker_packs usp ON sp.id = usp.pack_id AND usp.user_id = $1
		WHERE sp.is_official = true OR usp.user_id IS NOT NULL
		ORDER BY sp.is_official DESC, sp.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var packs []*models.StickerPack
	for rows.Next() {
		pack := &models.StickerPack{}
		err := rows.Scan(&pack.ID, &pack.Name, &pack.Description, &pack.CoverURL, &pack.IsOfficial, &pack.CreatorID, &pack.CreatedAt, &pack.UpdatedAt)
		if err != nil {
			continue
		}
		packs = append(packs, pack)
	}

	return packs, nil
}

// GetPack returns a sticker pack with all its stickers
func (r *Repository) GetPack(ctx context.Context, packID uuid.UUID) (*models.StickerPack, error) {
	pack := &models.StickerPack{}
	err := r.db.QueryRow(ctx, `
		SELECT id, name, description, cover_url, is_official, creator_id, created_at, updated_at
		FROM sticker_packs WHERE id = $1
	`, packID).Scan(&pack.ID, &pack.Name, &pack.Description, &pack.CoverURL, &pack.IsOfficial, &pack.CreatorID, &pack.CreatedAt, &pack.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPackNotFound
		}
		return nil, err
	}

	// Load stickers
	rows, err := r.db.Query(ctx, `
		SELECT id, pack_id, emoji, file_url, file_type, width, height, created_at
		FROM stickers WHERE pack_id = $1 ORDER BY created_at
	`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		sticker := &models.Sticker{}
		err := rows.Scan(&sticker.ID, &sticker.PackID, &sticker.Emoji, &sticker.FileURL, &sticker.FileType, &sticker.Width, &sticker.Height, &sticker.CreatedAt)
		if err != nil {
			continue
		}
		pack.Stickers = append(pack.Stickers, sticker)
	}

	return pack, nil
}

// GetOfficialPacks returns all official sticker packs
func (r *Repository) GetOfficialPacks(ctx context.Context) ([]*models.StickerPack, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, description, cover_url, is_official, creator_id, created_at, updated_at
		FROM sticker_packs WHERE is_official = true ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var packs []*models.StickerPack
	for rows.Next() {
		pack := &models.StickerPack{}
		err := rows.Scan(&pack.ID, &pack.Name, &pack.Description, &pack.CoverURL, &pack.IsOfficial, &pack.CreatorID, &pack.CreatedAt, &pack.UpdatedAt)
		if err != nil {
			continue
		}
		packs = append(packs, pack)
	}

	return packs, nil
}

// CreatePack creates a new sticker pack
func (r *Repository) CreatePack(ctx context.Context, creatorID uuid.UUID, name, description string, isOfficial bool) (*models.StickerPack, error) {
	pack := &models.StickerPack{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO sticker_packs (name, description, is_official, creator_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, description, cover_url, is_official, creator_id, created_at, updated_at
	`, name, description, isOfficial, creatorID).Scan(
		&pack.ID, &pack.Name, &pack.Description, &pack.CoverURL, &pack.IsOfficial, &pack.CreatorID, &pack.CreatedAt, &pack.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Auto-add to user's packs
	_, _ = r.db.Exec(ctx, `
		INSERT INTO user_sticker_packs (user_id, pack_id) VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, creatorID, pack.ID)

	return pack, nil
}

// AddSticker adds a sticker to a pack
func (r *Repository) AddSticker(ctx context.Context, packID uuid.UUID, emoji, fileURL, fileType string, width, height int) (*models.Sticker, error) {
	sticker := &models.Sticker{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO stickers (pack_id, emoji, file_url, file_type, width, height)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, pack_id, emoji, file_url, file_type, width, height, created_at
	`, packID, emoji, fileURL, fileType, width, height).Scan(
		&sticker.ID, &sticker.PackID, &sticker.Emoji, &sticker.FileURL, &sticker.FileType, &sticker.Width, &sticker.Height, &sticker.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Update pack cover if it's the first sticker
	_, _ = r.db.Exec(ctx, `
		UPDATE sticker_packs SET cover_url = $1 WHERE id = $2 AND cover_url IS NULL
	`, fileURL, packID)

	return sticker, nil
}

// AddPackToUser adds a sticker pack to user's collection
func (r *Repository) AddPackToUser(ctx context.Context, userID, packID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_sticker_packs (user_id, pack_id) VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, userID, packID)
	return err
}

// RemovePackFromUser removes a sticker pack from user's collection
func (r *Repository) RemovePackFromUser(ctx context.Context, userID, packID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM user_sticker_packs WHERE user_id = $1 AND pack_id = $2
	`, userID, packID)
	return err
}

// GetUserPacks returns user's saved sticker packs with stickers
func (r *Repository) GetUserPacks(ctx context.Context, userID uuid.UUID) ([]*models.StickerPack, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sp.id, sp.name, sp.description, sp.cover_url, sp.is_official, sp.creator_id, sp.created_at, sp.updated_at
		FROM sticker_packs sp
		JOIN user_sticker_packs usp ON sp.id = usp.pack_id
		WHERE usp.user_id = $1
		ORDER BY usp.sort_order, usp.added_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var packs []*models.StickerPack
	for rows.Next() {
		pack := &models.StickerPack{}
		err := rows.Scan(&pack.ID, &pack.Name, &pack.Description, &pack.CoverURL, &pack.IsOfficial, &pack.CreatorID, &pack.CreatedAt, &pack.UpdatedAt)
		if err != nil {
			continue
		}
		// Load stickers for each pack
		pack.Stickers, _ = r.getPackStickers(ctx, pack.ID)
		packs = append(packs, pack)
	}

	return packs, nil
}

func (r *Repository) getPackStickers(ctx context.Context, packID uuid.UUID) ([]*models.Sticker, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, pack_id, emoji, file_url, file_type, width, height, created_at
		FROM stickers WHERE pack_id = $1 ORDER BY created_at
	`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []*models.Sticker
	for rows.Next() {
		s := &models.Sticker{}
		if err := rows.Scan(&s.ID, &s.PackID, &s.Emoji, &s.FileURL, &s.FileType, &s.Width, &s.Height, &s.CreatedAt); err != nil {
			continue
		}
		stickers = append(stickers, s)
	}
	return stickers, nil
}

// GetSticker returns a single sticker by ID
func (r *Repository) GetSticker(ctx context.Context, stickerID uuid.UUID) (*models.Sticker, error) {
	sticker := &models.Sticker{}
	err := r.db.QueryRow(ctx, `
		SELECT id, pack_id, emoji, file_url, file_type, width, height, created_at
		FROM stickers WHERE id = $1
	`, stickerID).Scan(&sticker.ID, &sticker.PackID, &sticker.Emoji, &sticker.FileURL, &sticker.FileType, &sticker.Width, &sticker.Height, &sticker.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStickerNotFound
		}
		return nil, err
	}
	return sticker, nil
}

// DeletePack deletes a sticker pack (only by owner or if official by admin)
func (r *Repository) DeletePack(ctx context.Context, packID, userID uuid.UUID) error {
	// Check ownership
	var creatorID *uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT creator_id FROM sticker_packs WHERE id = $1`, packID).Scan(&creatorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPackNotFound
		}
		return err
	}

	if creatorID == nil || *creatorID != userID {
		return ErrNotOwner
	}

	_, err = r.db.Exec(ctx, `DELETE FROM sticker_packs WHERE id = $1`, packID)
	return err
}
