package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/user/bla-back/internal/cache"
	"github.com/user/bla-back/internal/models"
	"github.com/user/bla-back/internal/stickers"
	"github.com/user/bla-back/internal/storage"
)

type StickersHandler struct {
	repo      *stickers.Repository
	storage   *storage.S3Storage
	cache     *cache.RedisCache
	validator *validator.Validate
}

func NewStickersHandler(repo *stickers.Repository, storage *storage.S3Storage, cache *cache.RedisCache) *StickersHandler {
	return &StickersHandler{
		repo:      repo,
		storage:   storage,
		cache:     cache,
		validator: validator.New(),
	}
}

// GetPacks returns all sticker packs available to user
func (h *StickersHandler) GetPacks(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	packs, err := h.repo.GetUserPacks(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get sticker packs")
		return
	}

	// If user has no packs, return official packs
	if len(packs) == 0 {
		packs, err = h.repo.GetOfficialPacks(r.Context())
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to get sticker packs")
			return
		}
		// Load stickers for official packs
		for _, pack := range packs {
			fullPack, _ := h.repo.GetPack(r.Context(), pack.ID)
			if fullPack != nil {
				pack.Stickers = fullPack.Stickers
			}
		}
	}

	if packs == nil {
		packs = []*models.StickerPack{}
	}

	respondJSON(w, http.StatusOK, packs)
}

// GetPack returns a specific sticker pack with all stickers
func (h *StickersHandler) GetPack(w http.ResponseWriter, r *http.Request) {
	packID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pack ID")
		return
	}

	pack, err := h.repo.GetPack(r.Context(), packID)
	if err != nil {
		if errors.Is(err, stickers.ErrPackNotFound) {
			respondError(w, http.StatusNotFound, "Pack not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to get pack")
		return
	}

	respondJSON(w, http.StatusOK, pack)
}

// CreatePack creates a new sticker pack
func (h *StickersHandler) CreatePack(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.CreateStickerPackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed")
		return
	}

	pack, err := h.repo.CreatePack(r.Context(), userID, req.Name, req.Description, false)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create pack")
		return
	}

	respondJSON(w, http.StatusCreated, pack)
}

// UploadSticker uploads a sticker to a pack
func (h *StickersHandler) UploadSticker(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	packID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pack ID")
		return
	}

	// Verify pack exists and user owns it (or it's official - for admins)
	pack, err := h.repo.GetPack(r.Context(), packID)
	if err != nil {
		if errors.Is(err, stickers.ErrPackNotFound) {
			respondError(w, http.StatusNotFound, "Pack not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to get pack")
		return
	}

	// Check ownership (skip for now, can add admin check later)
	if pack.CreatorID != nil && *pack.CreatorID != userID && !pack.IsOfficial {
		respondError(w, http.StatusForbidden, "Not the pack owner")
		return
	}

	// Limit upload size to 512KB for stickers
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)

	if err := r.ParseMultipartForm(512 << 10); err != nil {
		respondError(w, http.StatusBadRequest, "File too large (max 512KB)")
		return
	}

	file, header, err := r.FormFile("sticker")
	if err != nil {
		respondError(w, http.StatusBadRequest, "No file provided")
		return
	}
	defer file.Close()

	emoji := r.FormValue("emoji")
	if emoji == "" {
		emoji = "😀"
	}

	// Determine file type
	contentType := header.Header.Get("Content-Type")
	var fileType string
	switch contentType {
	case "application/gzip", "application/x-tgsticker":
		fileType = "tgs"
	case "image/webp":
		fileType = "webp"
	case "image/png":
		fileType = "png"
	case "video/webm":
		fileType = "webm"
	default:
		// Check extension
		ext := strings.ToLower(header.Filename[len(header.Filename)-4:])
		switch ext {
		case ".tgs":
			fileType = "tgs"
			contentType = "application/gzip"
		case "webm":
			fileType = "webm"
			contentType = "video/webm"
		case "webp":
			fileType = "webp"
			contentType = "image/webp"
		case ".png":
			fileType = "png"
			contentType = "image/png"
		default:
			respondError(w, http.StatusBadRequest, "Invalid file type. Use .tgs, .webm, .webp, or .png")
			return
		}
	}

	// Upload to S3
	folder := "stickers/" + packID.String()
	fileURL, err := h.storage.Upload(r.Context(), folder, header.Filename, contentType, file)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to upload sticker")
		return
	}

	// Add to database
	sticker, err := h.repo.AddSticker(r.Context(), packID, emoji, fileURL, fileType, 512, 512)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to save sticker")
		return
	}

	respondJSON(w, http.StatusCreated, sticker)
}

// AddPackToCollection adds a sticker pack to user's collection
func (h *StickersHandler) AddPackToCollection(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	packID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pack ID")
		return
	}

	err = h.repo.AddPackToUser(r.Context(), userID, packID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to add pack")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Pack added"})
}

// RemovePackFromCollection removes a sticker pack from user's collection
func (h *StickersHandler) RemovePackFromCollection(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	packID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pack ID")
		return
	}

	err = h.repo.RemovePackFromUser(r.Context(), userID, packID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to remove pack")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Pack removed"})
}

// DeletePack deletes a sticker pack
func (h *StickersHandler) DeletePack(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	packID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid pack ID")
		return
	}

	err = h.repo.DeletePack(r.Context(), packID, userID)
	if err != nil {
		if errors.Is(err, stickers.ErrPackNotFound) {
			respondError(w, http.StatusNotFound, "Pack not found")
			return
		}
		if errors.Is(err, stickers.ErrNotOwner) {
			respondError(w, http.StatusForbidden, "Not the pack owner")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to delete pack")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Pack deleted"})
}

// ProxySticker proxies sticker files from S3 to avoid CORS issues
func (h *StickersHandler) ProxySticker(w http.ResponseWriter, r *http.Request) {
	stickerID, err := uuid.Parse(r.PathValue("stickerId"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid sticker ID")
		return
	}

	// Try to get sticker info from cache first
	var sticker *models.Sticker
	cacheKey := cache.StickerKey(stickerID.String())

	if h.cache != nil {
		var cached models.Sticker
		if err := h.cache.GetJSON(r.Context(), cacheKey, &cached); err == nil {
			sticker = &cached
		}
	}

	// If not in cache, get from database
	if sticker == nil {
		sticker, err = h.repo.GetSticker(r.Context(), stickerID)
		if err != nil {
			respondError(w, http.StatusNotFound, "Sticker not found")
			return
		}
		// Cache the metadata
		if h.cache != nil {
			h.cache.SetJSON(r.Context(), cacheKey, sticker, cache.StickerFileTTL)
		}
	}

	// Fetch from S3
	resp, err := http.Get(sticker.FileURL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch sticker")
		return
	}
	defer resp.Body.Close()

	// Set content type based on file type
	switch sticker.FileType {
	case "tgs":
		w.Header().Set("Content-Type", "application/gzip")
	case "webp":
		w.Header().Set("Content-Type", "image/webp")
	case "png":
		w.Header().Set("Content-Type", "image/png")
	case "webm":
		w.Header().Set("Content-Type", "video/webm")
	}

	// Long cache - stickers don't change
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	// Stream the response
	io.Copy(w, resp.Body)
}
