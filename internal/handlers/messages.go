package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/user/bla-back/internal/messages"
	"github.com/user/bla-back/internal/models"
	"github.com/user/bla-back/internal/realtime"
	"github.com/user/bla-back/internal/storage"
)

type MessagesHandler struct {
	repo      *messages.Repository
	rt        *realtime.Node
	storage   *storage.S3Storage
	validator *validator.Validate
}

func NewMessagesHandler(repo *messages.Repository, rt *realtime.Node, storage *storage.S3Storage) *MessagesHandler {
	return &MessagesHandler{
		repo:      repo,
		rt:        rt,
		storage:   storage,
		validator: validator.New(),
	}
}

// GetConversations returns all conversations for the user
func (h *MessagesHandler) GetConversations(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	conversations, err := h.repo.GetUserConversations(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get conversations")
		return
	}

	if conversations == nil {
		conversations = []*models.ConversationWithDetails{}
	}

	respondJSON(w, http.StatusOK, conversations)
}

// GetOrCreateDM gets or creates a DM with another user
func (h *MessagesHandler) GetOrCreateDM(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.CreateDMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	otherUserID, err := uuid.Parse(req.UserID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	if userID == otherUserID {
		respondError(w, http.StatusBadRequest, "Cannot create DM with yourself")
		return
	}

	conv, err := h.repo.GetOrCreateDM(r.Context(), userID, otherUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create conversation")
		return
	}

	respondJSON(w, http.StatusOK, conv)
}

// GetConversation returns a single conversation
func (h *MessagesHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	conv, err := h.repo.GetConversation(r.Context(), convID, userID)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		if errors.Is(err, messages.ErrConversationNotFound) {
			respondError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to get conversation")
		return
	}

	respondJSON(w, http.StatusOK, conv)
}

// GetMessages returns messages for a conversation
func (h *MessagesHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	msgs, err := h.repo.GetMessages(r.Context(), convID, userID, limit, offset)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to get messages")
		return
	}

	if msgs == nil {
		msgs = []*models.Message{}
	}

	respondJSON(w, http.StatusOK, msgs)
}

// SendMessage sends a message to a conversation
func (h *MessagesHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	var req models.SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Must have content or attachments
	if req.Content == "" && len(req.AttachmentIDs) == 0 {
		respondError(w, http.StatusBadRequest, "Message must have content or attachments")
		return
	}

	// Parse attachment IDs
	var attachmentIDs []uuid.UUID
	for _, idStr := range req.AttachmentIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			respondError(w, http.StatusBadRequest, "Invalid attachment ID")
			return
		}
		attachmentIDs = append(attachmentIDs, id)
	}

	msg, err := h.repo.SendMessageWithAttachments(r.Context(), convID, userID, req.Content, attachmentIDs)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to send message")
		return
	}

	// Broadcast to all participants via Centrifuge
	participantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(participantIDs, "MESSAGE_CREATE", &models.MessageCreateEvent{
		Message:        msg,
		ConversationID: convID,
	})

	respondJSON(w, http.StatusCreated, msg)
}

// UploadAttachment uploads a file attachment
func (h *MessagesHandler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// Limit upload size to 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		respondError(w, http.StatusBadRequest, "File too large (max 10MB)")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "No file provided")
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Determine attachment type
	attachType := "file"
	if isImageType(contentType) {
		attachType = "image"
	}

	// Upload to S3
	folder := "attachments/" + userID.String()
	fileURL, err := h.storage.Upload(r.Context(), folder, header.Filename, contentType, file)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to upload file")
		return
	}

	// Create attachment record (without message_id for now)
	attachment, err := h.repo.CreateAttachment(r.Context(), userID, attachType, fileURL, header.Filename, header.Size)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create attachment")
		return
	}

	respondJSON(w, http.StatusCreated, attachment)
}

func isImageType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// CreateGroup creates a new group conversation
func (h *MessagesHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.ParticipantIDs) == 0 {
		respondError(w, http.StatusBadRequest, "At least one participant required")
		return
	}

	// Parse participant IDs
	var participantIDs []uuid.UUID
	for _, idStr := range req.ParticipantIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			respondError(w, http.StatusBadRequest, "Invalid participant ID")
			return
		}
		participantIDs = append(participantIDs, id)
	}

	// Generate default name if not provided
	name := req.Name
	if name == "" {
		name = "Group Chat"
	}

	conv, err := h.repo.CreateGroup(r.Context(), userID, name, participantIDs)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}

	// Notify all participants about the new group
	allParticipantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), conv.ID)
	h.rt.PublishToUsers(allParticipantIDs, "CONVERSATION_CREATE", conv)

	respondJSON(w, http.StatusCreated, conv)
}

// AddParticipants adds participants to a group conversation
func (h *MessagesHandler) AddParticipants(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	var req models.AddParticipantsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.UserIDs) == 0 {
		respondError(w, http.StatusBadRequest, "At least one user required")
		return
	}

	// Parse user IDs
	var userIDs []uuid.UUID
	for _, idStr := range req.UserIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			respondError(w, http.StatusBadRequest, "Invalid user ID")
			return
		}
		userIDs = append(userIDs, id)
	}

	err = h.repo.AddParticipants(r.Context(), convID, userID, userIDs)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		if errors.Is(err, messages.ErrConversationNotFound) {
			respondError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to add participants")
		return
	}

	// Get updated conversation
	conv, err := h.repo.GetConversation(r.Context(), convID, userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get conversation")
		return
	}

	// Notify all participants (including new ones) about the update
	allParticipantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(allParticipantIDs, "CONVERSATION_UPDATE", conv)

	// Also send CONVERSATION_CREATE to new participants so they see it in their list
	h.rt.PublishToUsers(userIDs, "CONVERSATION_CREATE", conv)

	respondJSON(w, http.StatusOK, conv)
}

// UploadGroupAvatar uploads an avatar for a group conversation
func (h *MessagesHandler) UploadGroupAvatar(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	// Limit upload size to 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

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

	contentType := header.Header.Get("Content-Type")
	if !isImageType(contentType) {
		respondError(w, http.StatusBadRequest, "Invalid image type")
		return
	}

	// Upload to S3
	folder := "groups/" + convID.String()
	avatarURL, err := h.storage.Upload(r.Context(), folder, header.Filename, contentType, file)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to upload avatar")
		return
	}

	// Update group avatar in database
	err = h.repo.UpdateGroupAvatar(r.Context(), convID, userID, avatarURL)
	if err != nil {
		if err.Error() == "only the group owner can update the avatar" {
			respondError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, messages.ErrConversationNotFound) {
			respondError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to update avatar")
		return
	}

	// Get updated conversation and notify participants
	conv, err := h.repo.GetConversation(r.Context(), convID, userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get conversation")
		return
	}

	allParticipantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(allParticipantIDs, "CONVERSATION_UPDATE", conv)

	respondJSON(w, http.StatusOK, conv)
}

// UpdateGroup updates group settings (name)
func (h *MessagesHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	var req models.UpdateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	err = h.repo.UpdateGroupName(r.Context(), convID, userID, req.Name)
	if err != nil {
		if err.Error() == "only the group owner can update the name" {
			respondError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, messages.ErrConversationNotFound) {
			respondError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}

	// Get updated conversation and notify participants
	conv, err := h.repo.GetConversation(r.Context(), convID, userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get conversation")
		return
	}

	allParticipantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(allParticipantIDs, "CONVERSATION_UPDATE", conv)

	respondJSON(w, http.StatusOK, conv)
}

// LeaveGroup removes the user from a group conversation
func (h *MessagesHandler) LeaveGroup(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	// Get participant IDs before leaving (to notify them)
	participantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)

	err = h.repo.LeaveGroup(r.Context(), convID, userID)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		if errors.Is(err, messages.ErrConversationNotFound) {
			respondError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		if err.Error() == "can only leave group conversations" {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to leave group")
		return
	}

	// Notify remaining participants about the update
	for _, pid := range participantIDs {
		if pid != userID {
			conv, err := h.repo.GetConversation(r.Context(), convID, pid)
			if err == nil {
				h.rt.PublishToUser(pid, "CONVERSATION_UPDATE", conv)
			}
			break
		}
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Left group successfully"})
}

// DeleteMessage deletes a message from a conversation
func (h *MessagesHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	messageID, err := uuid.Parse(r.PathValue("messageId"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid message ID")
		return
	}

	err = h.repo.DeleteMessage(r.Context(), convID, messageID, userID)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		if errors.Is(err, messages.ErrMessageNotFound) {
			respondError(w, http.StatusNotFound, "Message not found")
			return
		}
		if err.Error() == "you can only delete your own messages" {
			respondError(w, http.StatusForbidden, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to delete message")
		return
	}

	// Notify all participants about the deleted message
	participantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(participantIDs, "MESSAGE_DELETE", &models.MessageDeleteEvent{
		MessageID:      messageID,
		ConversationID: convID,
	})

	respondJSON(w, http.StatusOK, map[string]string{"message": "Message deleted"})
}

// AddReaction adds a reaction to a message
func (h *MessagesHandler) AddReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	messageID, err := uuid.Parse(r.PathValue("messageId"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid message ID")
		return
	}

	var req models.AddReactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Emoji is required")
		return
	}

	reaction, err := h.repo.AddReaction(r.Context(), convID, messageID, userID, req.Emoji)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		if errors.Is(err, messages.ErrMessageNotFound) {
			respondError(w, http.StatusNotFound, "Message not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to add reaction")
		return
	}

	// Notify all participants about the new reaction
	participantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(participantIDs, "REACTION_ADD", &models.ReactionAddEvent{
		Reaction:       reaction,
		MessageID:      messageID,
		ConversationID: convID,
	})

	respondJSON(w, http.StatusOK, reaction)
}

// RemoveReaction removes a reaction from a message
func (h *MessagesHandler) RemoveReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid conversation ID")
		return
	}

	messageID, err := uuid.Parse(r.PathValue("messageId"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid message ID")
		return
	}

	emoji := r.PathValue("emoji")
	if emoji == "" {
		respondError(w, http.StatusBadRequest, "Emoji is required")
		return
	}

	err = h.repo.RemoveReaction(r.Context(), convID, messageID, userID, emoji)
	if err != nil {
		if errors.Is(err, messages.ErrNotParticipant) {
			respondError(w, http.StatusForbidden, "Not a participant")
			return
		}
		if err.Error() == "reaction not found" {
			respondError(w, http.StatusNotFound, "Reaction not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to remove reaction")
		return
	}

	// Notify all participants about the removed reaction
	participantIDs, _ := h.repo.GetConversationParticipantIDs(r.Context(), convID)
	h.rt.PublishToUsers(participantIDs, "REACTION_REMOVE", &models.ReactionRemoveEvent{
		MessageID:      messageID,
		ConversationID: convID,
		UserID:         userID,
		Emoji:          emoji,
	})

	respondJSON(w, http.StatusOK, map[string]string{"message": "Reaction removed"})
}
