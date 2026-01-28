package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/user/bla-back/internal/calls"
	"github.com/user/bla-back/internal/models"
	"github.com/user/bla-back/internal/realtime"
)

type CallsHandler struct {
	callsRepo *calls.Repository
	voice     *calls.VoiceService
	usersRepo UsersRepository
	notifier  *realtime.Notifier
	convRepo  ConversationRepository
	msgRepo   MessagesRepository
}

type UsersRepository interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (*models.User, error)
}

type ConversationRepository interface {
	GetParticipantIDs(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error)
}

type MessagesRepository interface {
	CreateCallMessage(ctx context.Context, convID, senderID uuid.UUID, content string) (*models.Message, error)
}

func NewCallsHandler(
	callsRepo *calls.Repository,
	voice *calls.VoiceService,
	usersRepo UsersRepository,
	notifier *realtime.Notifier,
	convRepo ConversationRepository,
	msgRepo MessagesRepository,
) *CallsHandler {
	return &CallsHandler{
		callsRepo: callsRepo,
		voice:     voice,
		usersRepo: usersRepo,
		notifier:  notifier,
		convRepo:  convRepo,
		msgRepo:   msgRepo,
	}
}

// Response types
type CallResponse struct {
	CallID     string `json:"call_id"`
	Token      string `json:"token"`
	LiveKitURL string `json:"livekit_url"`
}

// broadcastCallState sends current call state to all conversation participants
func (h *CallsHandler) broadcastCallState(ctx context.Context, conversationID uuid.UUID) {
	participantIDs, err := h.convRepo.GetParticipantIDs(ctx, conversationID)
	if err != nil {
		log.Printf("Failed to get conversation participants: %v", err)
		return
	}

	// Get current call state
	call, err := h.callsRepo.GetActiveCallForConversation(ctx, conversationID)

	event := models.CallStateEvent{
		ConversationID: conversationID,
		CallID:         nil,
		Participants:   []uuid.UUID{},
	}

	if err == nil && call != nil {
		event.CallID = &call.ID
		// Get active participants
		participants, _ := h.callsRepo.GetActiveParticipants(ctx, call.ID)
		event.Participants = participants
	}

	h.notifier.NotifyUsers(participantIDs, "CALL_STATE", event)
}

// StartCall starts a new call or joins existing one
func (h *CallsHandler) StartCall(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	conversationID, err := uuid.Parse(req.ConversationID)
	if err != nil {
		http.Error(w, "Invalid conversation_id", http.StatusBadRequest)
		return
	}

	// Check if user is already in another call
	existingCall, err := h.callsRepo.IsUserInCall(r.Context(), userID)
	if err != nil {
		log.Printf("IsUserInCall error: %v", err)
		http.Error(w, "Failed to check call status", http.StatusInternalServerError)
		return
	}
	if existingCall != nil && existingCall.ConversationID != conversationID {
		http.Error(w, "Already in another call", http.StatusConflict)
		return
	}

	// Check if there's already an active call in this conversation
	call, err := h.callsRepo.GetActiveCallForConversation(r.Context(), conversationID)
	if err != nil && err != pgx.ErrNoRows {
		log.Printf("GetActiveCallForConversation error: %v", err)
		http.Error(w, "Failed to check existing call", http.StatusInternalServerError)
		return
	}

	if call == nil {
		// Start new call
		call, err = h.callsRepo.StartCall(r.Context(), conversationID, userID)
		if err != nil {
			log.Printf("StartCall error: %v", err)
			http.Error(w, "Failed to start call", http.StatusInternalServerError)
			return
		}
	} else {
		// Join existing call (if not already in it)
		if err := h.callsRepo.JoinCall(r.Context(), call.ID, userID); err != nil {
			log.Printf("JoinCall error: %v", err)
			http.Error(w, "Failed to join call", http.StatusInternalServerError)
			return
		}
	}

	// Get username for LiveKit
	user, err := h.usersRepo.GetUserByID(r.Context(), userID)
	if err != nil {
		log.Printf("GetUserByID error: %v", err)
		http.Error(w, "Failed to get user", http.StatusInternalServerError)
		return
	}

	username := ""
	if user.Username != nil {
		username = *user.Username
	}

	// Generate voice token
	roomName := "call-" + call.ID.String()
	token, err := h.voice.GenerateToken(roomName, userID.String(), username)
	if err != nil {
		log.Printf("GenerateToken error: %v", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	// Broadcast updated call state to all conversation participants
	h.broadcastCallState(r.Context(), conversationID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CallResponse{
		CallID:     call.ID.String(),
		Token:      token,
		LiveKitURL: h.voice.GetWebSocketURL(),
	})
}

// JoinCall joins an existing call
func (h *CallsHandler) JoinCall(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CallID string `json:"call_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	callID, err := uuid.Parse(req.CallID)
	if err != nil {
		http.Error(w, "Invalid call_id", http.StatusBadRequest)
		return
	}

	// Get call
	call, err := h.callsRepo.GetCallWithParticipants(r.Context(), callID)
	if err != nil {
		http.Error(w, "Call not found", http.StatusNotFound)
		return
	}

	if call.EndedAt != nil {
		http.Error(w, "Call has ended", http.StatusGone)
		return
	}

	// Join call
	if err := h.callsRepo.JoinCall(r.Context(), callID, userID); err != nil {
		log.Printf("JoinCall error: %v", err)
		http.Error(w, "Failed to join call", http.StatusInternalServerError)
		return
	}

	// Get username for LiveKit
	user, err := h.usersRepo.GetUserByID(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to get user", http.StatusInternalServerError)
		return
	}

	username := ""
	if user.Username != nil {
		username = *user.Username
	}

	// Generate voice token
	roomName := "call-" + call.ID.String()
	token, err := h.voice.GenerateToken(roomName, userID.String(), username)
	if err != nil {
		log.Printf("GenerateToken error: %v", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	// Broadcast updated call state
	h.broadcastCallState(r.Context(), call.ConversationID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CallResponse{
		CallID:     call.ID.String(),
		Token:      token,
		LiveKitURL: h.voice.GetWebSocketURL(),
	})
}

// LeaveCall leaves a call
func (h *CallsHandler) LeaveCall(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CallID string `json:"call_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	callID, err := uuid.Parse(req.CallID)
	if err != nil {
		http.Error(w, "Invalid call_id", http.StatusBadRequest)
		return
	}

	// Get call for conversation ID
	call, err := h.callsRepo.GetCallWithParticipants(r.Context(), callID)
	if err != nil {
		http.Error(w, "Call not found", http.StatusNotFound)
		return
	}

	// Leave call
	if err := h.callsRepo.LeaveCall(r.Context(), callID, userID); err != nil {
		http.Error(w, "Failed to leave call", http.StatusInternalServerError)
		return
	}

	// Check if call is now empty
	count, _ := h.callsRepo.GetActiveParticipantCount(r.Context(), callID)
	if count == 0 {
		// End the call and create call message
		// EndCall returns nil if call was already ended (race condition)
		callInfo, err := h.callsRepo.EndCall(r.Context(), callID)
		if err != nil {
			log.Printf("EndCall error: %v", err)
		} else if callInfo != nil {
			// Only create message if we actually ended the call (not already ended)
			h.createCallMessage(r.Context(), callInfo)
		}
	}

	// Broadcast updated call state (will show no call if ended)
	h.broadcastCallState(r.Context(), call.ConversationID)

	w.WriteHeader(http.StatusNoContent)
}

// createCallMessage creates a system message for a completed call
func (h *CallsHandler) createCallMessage(ctx context.Context, info *calls.CallEndInfo) {
	if info == nil {
		return
	}

	// Build participant IDs as strings
	participants := make([]string, len(info.Participants))
	for i, p := range info.Participants {
		participants[i] = p.String()
	}

	// Determine call status
	status := "completed"
	if info.Duration < 5 && len(info.Participants) == 1 {
		status = "missed" // Only caller, very short = likely missed
	}

	// Create JSON content
	content := models.CallMessageContent{
		CallID:       info.CallID.String(),
		Duration:     info.Duration,
		Participants: participants,
		Status:       status,
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		log.Printf("Failed to marshal call content: %v", err)
		return
	}

	// Create the message (sender is the one who started the call)
	msg, err := h.msgRepo.CreateCallMessage(ctx, info.ConversationID, info.StartedBy, string(contentJSON))
	if err != nil {
		log.Printf("Failed to create call message: %v", err)
		return
	}

	// Notify all conversation participants about the new message
	participantIDs, err := h.convRepo.GetParticipantIDs(ctx, info.ConversationID)
	if err != nil {
		log.Printf("Failed to get participant IDs: %v", err)
		return
	}

	h.notifier.NotifyUsers(participantIDs, "MESSAGE_CREATE", map[string]interface{}{
		"message":         msg,
		"conversation_id": info.ConversationID,
	})

	log.Printf("Created call message: duration=%ds, participants=%d, status=%s",
		info.Duration, len(info.Participants), status)
}

// GetActiveCall returns the active call for a conversation
func (h *CallsHandler) GetActiveCall(w http.ResponseWriter, r *http.Request) {
	conversationID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid conversation_id", http.StatusBadRequest)
		return
	}

	call, err := h.callsRepo.GetActiveCallForConversation(r.Context(), conversationID)
	if err == pgx.ErrNoRows || call == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(w, "Failed to get call", http.StatusInternalServerError)
		return
	}

	// Get participants
	participants, _ := h.callsRepo.GetActiveParticipants(r.Context(), call.ID)

	participantStrings := make([]string, len(participants))
	for i, p := range participants {
		participantStrings[i] = p.String()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"call_id":      call.ID.String(),
		"started_at":   call.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		"participants": participantStrings,
	})
}
