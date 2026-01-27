package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/user/bla-back/internal/friends"
	"github.com/user/bla-back/internal/models"
	"github.com/user/bla-back/internal/realtime"
)

type FriendsHandler struct {
	repo      *friends.Repository
	rt        *realtime.Node
	validator *validator.Validate
}

func NewFriendsHandler(repo *friends.Repository, rt *realtime.Node) *FriendsHandler {
	return &FriendsHandler{
		repo:      repo,
		rt:        rt,
		validator: validator.New(),
	}
}

// SendRequest sends a friend request by user ID
func (h *FriendsHandler) SendRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.SendFriendRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed")
		return
	}

	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	friendReq, err := h.repo.SendRequest(r.Context(), userID, targetID)
	if err != nil {
		switch {
		case errors.Is(err, friends.ErrCannotAddSelf):
			respondError(w, http.StatusBadRequest, "Cannot send friend request to yourself")
		case errors.Is(err, friends.ErrAlreadyFriends):
			respondError(w, http.StatusConflict, "Already friends")
		case errors.Is(err, friends.ErrRequestAlreadyExists):
			respondError(w, http.StatusConflict, "Friend request already sent")
		case errors.Is(err, friends.ErrUserBlocked):
			respondError(w, http.StatusForbidden, "Cannot send request to this user")
		default:
			respondError(w, http.StatusInternalServerError, "Failed to send friend request")
		}
		return
	}

	// Check if auto-accepted (became friends)
	if friendReq.Status == "accepted" {
		// Both users became friends - send RELATIONSHIP_ADD to both
		senderFriend, _ := h.repo.GetFriendByUserID(r.Context(), userID, targetID)
		targetFriend, _ := h.repo.GetFriendByUserID(r.Context(), targetID, userID)

		if senderFriend != nil {
			h.rt.PublishToUser(userID, "RELATIONSHIP_ADD", &models.RelationshipAddEvent{Friend: senderFriend})
		}
		if targetFriend != nil {
			h.rt.PublishToUser(targetID, "RELATIONSHIP_ADD", &models.RelationshipAddEvent{Friend: targetFriend})
		}
	} else {
		// Send FRIEND_REQUEST_CREATE to target user
		reqWithUser, _ := h.repo.GetRequestWithUser(r.Context(), friendReq.ID, targetID)
		if reqWithUser != nil {
			h.rt.PublishToUser(targetID, "FRIEND_REQUEST_CREATE", &models.FriendRequestCreateEvent{Request: reqWithUser})
		}
	}

	respondJSON(w, http.StatusCreated, friendReq)
}

// SendRequestByUsername sends a friend request by username
func (h *FriendsHandler) SendRequestByUsername(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.SendFriendRequestByUsernameDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, "Validation failed")
		return
	}

	targetUser, err := h.repo.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		respondError(w, http.StatusNotFound, "User not found")
		return
	}

	friendReq, err := h.repo.SendRequest(r.Context(), userID, targetUser.ID)
	if err != nil {
		switch {
		case errors.Is(err, friends.ErrCannotAddSelf):
			respondError(w, http.StatusBadRequest, "Cannot send friend request to yourself")
		case errors.Is(err, friends.ErrAlreadyFriends):
			respondError(w, http.StatusConflict, "Already friends")
		case errors.Is(err, friends.ErrRequestAlreadyExists):
			respondError(w, http.StatusConflict, "Friend request already sent")
		case errors.Is(err, friends.ErrUserBlocked):
			respondError(w, http.StatusForbidden, "Cannot send request to this user")
		default:
			respondError(w, http.StatusInternalServerError, "Failed to send friend request")
		}
		return
	}

	// Check if auto-accepted (became friends)
	if friendReq.Status == "accepted" {
		senderFriend, _ := h.repo.GetFriendByUserID(r.Context(), userID, targetUser.ID)
		targetFriend, _ := h.repo.GetFriendByUserID(r.Context(), targetUser.ID, userID)

		if senderFriend != nil {
			h.rt.PublishToUser(userID, "RELATIONSHIP_ADD", &models.RelationshipAddEvent{Friend: senderFriend})
		}
		if targetFriend != nil {
			h.rt.PublishToUser(targetUser.ID, "RELATIONSHIP_ADD", &models.RelationshipAddEvent{Friend: targetFriend})
		}
	} else {
		reqWithUser, _ := h.repo.GetRequestWithUser(r.Context(), friendReq.ID, targetUser.ID)
		if reqWithUser != nil {
			h.rt.PublishToUser(targetUser.ID, "FRIEND_REQUEST_CREATE", &models.FriendRequestCreateEvent{Request: reqWithUser})
		}
	}

	respondJSON(w, http.StatusCreated, friendReq)
}

// AcceptRequest accepts a friend request
func (h *FriendsHandler) AcceptRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	requestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request ID")
		return
	}

	// Get request before accepting to know the sender
	request, err := h.repo.GetRequest(r.Context(), requestID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Friend request not found")
		return
	}

	friendReq, err := h.repo.AcceptRequest(r.Context(), userID, requestID)
	if err != nil {
		if errors.Is(err, friends.ErrRequestNotFound) {
			respondError(w, http.StatusNotFound, "Friend request not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to accept request")
		return
	}

	// Send RELATIONSHIP_ADD to both users
	senderFriend, _ := h.repo.GetFriendByUserID(r.Context(), request.FromUserID, userID)
	accepterFriend, _ := h.repo.GetFriendByUserID(r.Context(), userID, request.FromUserID)

	if senderFriend != nil {
		h.rt.PublishToUser(request.FromUserID, "RELATIONSHIP_ADD", &models.RelationshipAddEvent{Friend: senderFriend})
	}
	if accepterFriend != nil {
		h.rt.PublishToUser(userID, "RELATIONSHIP_ADD", &models.RelationshipAddEvent{Friend: accepterFriend})
	}

	// Send FRIEND_REQUEST_DELETE to sender (their outgoing request is gone)
	h.rt.PublishToUser(request.FromUserID, "FRIEND_REQUEST_DELETE", &models.FriendRequestDeleteEvent{
		RequestID: requestID,
		UserID:    userID,
	})

	respondJSON(w, http.StatusOK, friendReq)
}

// DeclineRequest declines a friend request
func (h *FriendsHandler) DeclineRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	requestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request ID")
		return
	}

	// Get request before declining
	request, _ := h.repo.GetRequest(r.Context(), requestID)

	err = h.repo.DeclineRequest(r.Context(), userID, requestID)
	if err != nil {
		if errors.Is(err, friends.ErrRequestNotFound) {
			respondError(w, http.StatusNotFound, "Friend request not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to decline request")
		return
	}

	// Notify sender that their request was deleted
	if request != nil {
		h.rt.PublishToUser(request.FromUserID, "FRIEND_REQUEST_DELETE", &models.FriendRequestDeleteEvent{
			RequestID: requestID,
			UserID:    userID,
		})
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Request declined"})
}

// CancelRequest cancels an outgoing friend request
func (h *FriendsHandler) CancelRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	requestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request ID")
		return
	}

	// Get request before cancelling
	request, _ := h.repo.GetRequest(r.Context(), requestID)

	err = h.repo.CancelRequest(r.Context(), userID, requestID)
	if err != nil {
		if errors.Is(err, friends.ErrRequestNotFound) {
			respondError(w, http.StatusNotFound, "Friend request not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to cancel request")
		return
	}

	// Notify target that the incoming request was deleted
	if request != nil {
		h.rt.PublishToUser(request.ToUserID, "FRIEND_REQUEST_DELETE", &models.FriendRequestDeleteEvent{
			RequestID: requestID,
			UserID:    userID,
		})
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Request cancelled"})
}

// RemoveFriend removes a friendship
func (h *FriendsHandler) RemoveFriend(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	friendID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid friend ID")
		return
	}

	err = h.repo.RemoveFriend(r.Context(), userID, friendID)
	if err != nil {
		if errors.Is(err, friends.ErrRequestNotFound) {
			respondError(w, http.StatusNotFound, "Friendship not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to remove friend")
		return
	}

	// Notify both users
	h.rt.PublishToUser(userID, "RELATIONSHIP_REMOVE", &models.RelationshipRemoveEvent{UserID: friendID})
	h.rt.PublishToUser(friendID, "RELATIONSHIP_REMOVE", &models.RelationshipRemoveEvent{UserID: userID})

	respondJSON(w, http.StatusOK, map[string]string{"message": "Friend removed"})
}

// GetFriends returns all friends
func (h *FriendsHandler) GetFriends(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	friendsList, err := h.repo.GetFriends(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get friends")
		return
	}

	if friendsList == nil {
		friendsList = []*models.FriendWithUser{}
	}

	respondJSON(w, http.StatusOK, friendsList)
}

// GetIncomingRequests returns incoming friend requests
func (h *FriendsHandler) GetIncomingRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	requests, err := h.repo.GetIncomingRequests(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get requests")
		return
	}

	if requests == nil {
		requests = []*models.FriendRequestWithUser{}
	}

	respondJSON(w, http.StatusOK, requests)
}

// GetOutgoingRequests returns outgoing friend requests
func (h *FriendsHandler) GetOutgoingRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	requests, err := h.repo.GetOutgoingRequests(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get requests")
		return
	}

	if requests == nil {
		requests = []*models.FriendRequestWithUser{}
	}

	respondJSON(w, http.StatusOK, requests)
}

// Block blocks a user
func (h *FriendsHandler) Block(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req models.BlockUserDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	block, err := h.repo.Block(r.Context(), userID, targetID)
	if err != nil {
		if errors.Is(err, friends.ErrCannotAddSelf) {
			respondError(w, http.StatusBadRequest, "Cannot block yourself")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to block user")
		return
	}

	respondJSON(w, http.StatusCreated, block)
}

// Unblock unblocks a user
func (h *FriendsHandler) Unblock(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	blockedID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	err = h.repo.Unblock(r.Context(), userID, blockedID)
	if err != nil {
		if errors.Is(err, friends.ErrBlockNotFound) {
			respondError(w, http.StatusNotFound, "Block not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to unblock user")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "User unblocked"})
}

// GetBlocks returns all blocked users
func (h *FriendsHandler) GetBlocks(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(uuid.UUID)
	if !ok {
		respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	blocks, err := h.repo.GetBlocks(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get blocks")
		return
	}

	if blocks == nil {
		blocks = []*models.BlockWithUser{}
	}

	respondJSON(w, http.StatusOK, blocks)
}
