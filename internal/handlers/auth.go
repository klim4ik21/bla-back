package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/user/bla-back/internal/auth"
	"github.com/user/bla-back/internal/models"
	"github.com/user/bla-back/internal/storage"
)

type AuthHandler struct {
	repo      *auth.Repository
	tokens    *auth.TokenService
	storage   *storage.S3Storage
	validator *validator.Validate
}

func NewAuthHandler(repo *auth.Repository, tokens *auth.TokenService, storage *storage.S3Storage) *AuthHandler {
	return &AuthHandler{
		repo:      repo,
		tokens:    tokens,
		storage:   storage,
		validator: validator.New(),
	}
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to process password")
		return
	}

	user, err := h.repo.CreateUser(r.Context(), req.Email, passwordHash)
	if err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			respondError(w, http.StatusConflict, "User with this email already exists")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}

	tokens, err := h.generateTokens(r, user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate tokens")
		return
	}

	respondJSON(w, http.StatusCreated, models.AuthResponse{
		User:         user,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}

	user, err := h.repo.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			respondError(w, http.StatusUnauthorized, "Invalid credentials")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to fetch user")
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		respondError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	tokens, err := h.generateTokens(r, user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate tokens")
		return
	}

	respondJSON(w, http.StatusOK, models.AuthResponse{
		User:         user,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	})
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req models.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}

	rt, err := h.repo.GetRefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Invalid refresh token")
		return
	}

	// Delete old refresh token
	if err := h.repo.DeleteRefreshToken(r.Context(), req.RefreshToken); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to invalidate old token")
		return
	}

	tokens, err := h.generateTokens(r, rt.UserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate tokens")
		return
	}

	respondJSON(w, http.StatusOK, tokens)
}

func (h *AuthHandler) SetUsername(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.SetUsernameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}

	user, err := h.repo.SetUsername(r.Context(), userID, req.Username)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameExists) {
			respondError(w, http.StatusConflict, "Username already taken")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to set username")
		return
	}

	respondJSON(w, http.StatusOK, user)
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	user, err := h.repo.GetUserByID(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusNotFound, "User not found")
		return
	}

	respondJSON(w, http.StatusOK, user)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req models.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	_ = h.repo.DeleteRefreshToken(r.Context(), req.RefreshToken)

	respondJSON(w, http.StatusOK, map[string]string{"message": "Logged out successfully"})
}

func (h *AuthHandler) generateTokens(r *http.Request, userID uuid.UUID) (*models.TokenResponse, error) {
	accessToken, err := h.tokens.GenerateAccessToken(userID)
	if err != nil {
		return nil, err
	}

	refreshToken, expiresAt, err := h.tokens.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	if err := h.repo.SaveRefreshToken(r.Context(), userID, refreshToken, expiresAt); err != nil {
		return nil, err
	}

	return &models.TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// UploadAvatar handles avatar image upload
func (h *AuthHandler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// Limit upload size to 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	// Parse multipart form
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		respondError(w, http.StatusBadRequest, "File too large (max 5MB)")
		return
	}

	file, header, err := r.FormFile("avatar")
	if err != nil {
		respondError(w, http.StatusBadRequest, "No file provided")
		return
	}
	defer file.Close()

	// Get content type
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Upload to S3
	avatarURL, err := h.storage.UploadAvatar(r.Context(), userID, header.Filename, contentType, file)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Get current user to delete old avatar if exists
	user, err := h.repo.GetUserByID(r.Context(), userID)
	if err == nil && user.AvatarURL != nil && *user.AvatarURL != "" {
		// Try to delete old avatar (ignore errors)
		_ = h.storage.Delete(r.Context(), *user.AvatarURL)
	}

	// Update user's avatar URL in database
	user, err = h.repo.SetAvatarURL(r.Context(), userID, avatarURL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update avatar")
		return
	}

	respondJSON(w, http.StatusOK, user)
}
