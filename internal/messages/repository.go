package messages

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/bla-back/internal/models"
)

var (
	ErrConversationNotFound = errors.New("conversation not found")
	ErrNotParticipant       = errors.New("not a participant of this conversation")
	ErrMessageNotFound      = errors.New("message not found")
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetOrCreateDM gets existing DM or creates a new one
func (r *Repository) GetOrCreateDM(ctx context.Context, userA, userB uuid.UUID) (*models.Conversation, error) {
	// Try to find existing DM
	var convID uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT c.id FROM conversations c
		JOIN conversation_participants cp1 ON c.id = cp1.conversation_id AND cp1.user_id = $1
		JOIN conversation_participants cp2 ON c.id = cp2.conversation_id AND cp2.user_id = $2
		WHERE c.type = 'dm'
	`, userA, userB).Scan(&convID)

	if err == nil {
		return r.GetConversation(ctx, convID, userA)
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// Create new DM
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (type) VALUES ('dm') RETURNING id
	`).Scan(&convID)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO conversation_participants (conversation_id, user_id) VALUES ($1, $2), ($1, $3)
	`, convID, userA, userB)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return r.GetConversation(ctx, convID, userA)
}

// GetConversation gets a conversation by ID
func (r *Repository) GetConversation(ctx context.Context, convID, userID uuid.UUID) (*models.Conversation, error) {
	// Verify user is participant
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, userID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotParticipant
	}

	conv := &models.Conversation{}
	err = r.db.QueryRow(ctx, `
		SELECT id, type, name, avatar_url, owner_id, created_at, updated_at FROM conversations WHERE id = $1
	`, convID).Scan(&conv.ID, &conv.Type, &conv.Name, &conv.AvatarURL, &conv.OwnerID, &conv.CreatedAt, &conv.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, err
	}

	// Get participants
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM users u
		JOIN conversation_participants cp ON u.id = cp.user_id
		WHERE cp.conversation_id = $1
	`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		user := &models.User{}
		err := rows.Scan(&user.ID, &user.Email, &user.Username, &user.AvatarURL, &user.Status, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return nil, err
		}
		conv.Participants = append(conv.Participants, user)
	}

	// Get last message
	lastMsg := &models.Message{}
	err = r.db.QueryRow(ctx, `
		SELECT m.id, m.conversation_id, m.sender_id, m.content, m.created_at, m.updated_at
		FROM messages m
		WHERE m.conversation_id = $1
		ORDER BY m.created_at DESC LIMIT 1
	`, convID).Scan(&lastMsg.ID, &lastMsg.ConversationID, &lastMsg.SenderID, &lastMsg.Content, &lastMsg.CreatedAt, &lastMsg.UpdatedAt)
	if err == nil {
		conv.LastMessage = lastMsg
	}

	return conv, nil
}

// GetUserConversations gets all conversations for a user
func (r *Repository) GetUserConversations(ctx context.Context, userID uuid.UUID) ([]*models.ConversationWithDetails, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT c.id, c.type, c.name, c.avatar_url, c.owner_id, c.updated_at
		FROM conversations c
		JOIN conversation_participants cp ON c.id = cp.conversation_id
		WHERE cp.user_id = $1
		ORDER BY c.updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conversations []*models.ConversationWithDetails
	for rows.Next() {
		conv := &models.ConversationWithDetails{}
		err := rows.Scan(&conv.ID, &conv.Type, &conv.Name, &conv.AvatarURL, &conv.OwnerID, &conv.UpdatedAt)
		if err != nil {
			return nil, err
		}
		conversations = append(conversations, conv)
	}

	// Load participants and last message for each
	for _, conv := range conversations {
		// Participants
		pRows, err := r.db.Query(ctx, `
			SELECT u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
			FROM users u
			JOIN conversation_participants cp ON u.id = cp.user_id
			WHERE cp.conversation_id = $1
		`, conv.ID)
		if err != nil {
			return nil, err
		}

		for pRows.Next() {
			user := &models.User{}
			err := pRows.Scan(&user.ID, &user.Email, &user.Username, &user.AvatarURL, &user.Status, &user.CreatedAt, &user.UpdatedAt)
			if err != nil {
				pRows.Close()
				return nil, err
			}
			conv.Participants = append(conv.Participants, user)
		}
		pRows.Close()

		// Last message
		lastMsg := &models.Message{}
		err = r.db.QueryRow(ctx, `
			SELECT m.id, m.conversation_id, m.sender_id, m.content, m.created_at, m.updated_at
			FROM messages m WHERE m.conversation_id = $1
			ORDER BY m.created_at DESC LIMIT 1
		`, conv.ID).Scan(&lastMsg.ID, &lastMsg.ConversationID, &lastMsg.SenderID, &lastMsg.Content, &lastMsg.CreatedAt, &lastMsg.UpdatedAt)
		if err == nil {
			conv.LastMessage = lastMsg
		}
	}

	return conversations, nil
}

// GetMessages gets messages for a conversation
func (r *Repository) GetMessages(ctx context.Context, convID, userID uuid.UUID, limit, offset int) ([]*models.Message, error) {
	// Single query: verify participant and get messages at once
	// If user is not a participant, this returns 0 rows
	rows, err := r.db.Query(ctx, `
		SELECT m.id, m.conversation_id, m.sender_id, COALESCE(m.type, 'text'), m.content, m.created_at, m.updated_at,
			   u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM messages m
		JOIN users u ON m.sender_id = u.id
		WHERE m.conversation_id = $1
		  AND EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
		ORDER BY m.created_at DESC
		LIMIT $3 OFFSET $4
	`, convID, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*models.Message
	for rows.Next() {
		msg := &models.Message{Sender: &models.User{}}
		err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Type, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt,
			&msg.Sender.ID, &msg.Sender.Email, &msg.Sender.Username, &msg.Sender.AvatarURL, &msg.Sender.Status, &msg.Sender.CreatedAt, &msg.Sender.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	// Load attachments and reactions for each message
	for _, msg := range messages {
		msg.Attachments = r.loadAttachments(ctx, msg.ID)
		msg.Reactions = r.loadReactions(ctx, msg.ID)
	}

	// Reverse to get chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// SendMessage sends a message to a conversation
func (r *Repository) SendMessage(ctx context.Context, convID, senderID uuid.UUID, content string) (*models.Message, error) {
	// Verify participant
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, senderID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotParticipant
	}

	msg := &models.Message{}
	err = r.db.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, sender_id, content)
		VALUES ($1, $2, $3)
		RETURNING id, conversation_id, sender_id, content, created_at, updated_at
	`, convID, senderID, content).Scan(
		&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Update conversation updated_at
	_, _ = r.db.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, convID)

	// Get sender info
	msg.Sender = &models.User{}
	_ = r.db.QueryRow(ctx, `
		SELECT id, email, username, avatar_url, status, created_at, updated_at
		FROM users WHERE id = $1
	`, senderID).Scan(
		&msg.Sender.ID, &msg.Sender.Email, &msg.Sender.Username, &msg.Sender.AvatarURL, &msg.Sender.Status, &msg.Sender.CreatedAt, &msg.Sender.UpdatedAt,
	)

	return msg, nil
}

// GetConversationParticipantIDs gets all participant IDs for a conversation
func (r *Repository) GetConversationParticipantIDs(ctx context.Context, convID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id FROM conversation_participants WHERE conversation_id = $1
	`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// CreateAttachment creates an attachment record (without message_id, for pre-upload)
func (r *Repository) CreateAttachment(ctx context.Context, uploaderID uuid.UUID, attachType, url, filename string, size int64) (*models.Attachment, error) {
	attachment := &models.Attachment{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO attachments (uploader_id, type, url, filename, size)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, type, url, filename, size, created_at
	`, uploaderID, attachType, url, filename, size).Scan(
		&attachment.ID, &attachment.Type, &attachment.URL, &attachment.Filename, &attachment.Size, &attachment.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return attachment, nil
}

// SendMessageWithAttachments creates a message and links attachments to it
func (r *Repository) SendMessageWithAttachments(ctx context.Context, convID, senderID uuid.UUID, content string, attachmentIDs []uuid.UUID) (*models.Message, error) {
	// Verify participant
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, senderID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotParticipant
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Create message
	msg := &models.Message{}
	err = tx.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, sender_id, content)
		VALUES ($1, $2, $3)
		RETURNING id, conversation_id, sender_id, content, created_at, updated_at
	`, convID, senderID, content).Scan(
		&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Link attachments to message (only if user owns them and they're not already linked)
	if len(attachmentIDs) > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE attachments
			SET message_id = $1
			WHERE id = ANY($2) AND uploader_id = $3 AND message_id IS NULL
		`, msg.ID, attachmentIDs, senderID)
		if err != nil {
			return nil, err
		}
	}

	// Update conversation updated_at
	_, _ = tx.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, convID)

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Get sender info
	msg.Sender = &models.User{}
	_ = r.db.QueryRow(ctx, `
		SELECT id, email, username, avatar_url, status, created_at, updated_at
		FROM users WHERE id = $1
	`, senderID).Scan(
		&msg.Sender.ID, &msg.Sender.Email, &msg.Sender.Username, &msg.Sender.AvatarURL, &msg.Sender.Status, &msg.Sender.CreatedAt, &msg.Sender.UpdatedAt,
	)

	// Load attachments
	msg.Attachments = r.loadAttachments(ctx, msg.ID)
	msg.Reactions = []*models.Reaction{} // New messages have no reactions

	return msg, nil
}

// loadAttachments loads attachments for a message
func (r *Repository) loadAttachments(ctx context.Context, messageID uuid.UUID) []*models.Attachment {
	rows, err := r.db.Query(ctx, `
		SELECT id, message_id, type, url, filename, size, width, height, created_at
		FROM attachments WHERE message_id = $1
	`, messageID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var attachments []*models.Attachment
	for rows.Next() {
		a := &models.Attachment{}
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Type, &a.URL, &a.Filename, &a.Size, &a.Width, &a.Height, &a.CreatedAt); err != nil {
			continue
		}
		attachments = append(attachments, a)
	}
	return attachments
}

// CreateGroup creates a new group conversation
func (r *Repository) CreateGroup(ctx context.Context, creatorID uuid.UUID, name string, participantIDs []uuid.UUID) (*models.Conversation, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Create conversation with owner
	var convID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (type, name, owner_id) VALUES ('group', $1, $2) RETURNING id
	`, name, creatorID).Scan(&convID)
	if err != nil {
		return nil, err
	}

	// Add creator as participant
	allParticipants := append([]uuid.UUID{creatorID}, participantIDs...)

	// Remove duplicates
	seen := make(map[uuid.UUID]bool)
	unique := []uuid.UUID{}
	for _, id := range allParticipants {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}

	// Add all participants
	for _, userID := range unique {
		_, err = tx.Exec(ctx, `
			INSERT INTO conversation_participants (conversation_id, user_id) VALUES ($1, $2)
		`, convID, userID)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return r.GetConversation(ctx, convID, creatorID)
}

// AddParticipants adds participants to a group conversation
func (r *Repository) AddParticipants(ctx context.Context, convID, requestingUserID uuid.UUID, userIDs []uuid.UUID) error {
	// Verify requesting user is a participant
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, requestingUserID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotParticipant
	}

	// Verify it's a group conversation
	var convType string
	err = r.db.QueryRow(ctx, `SELECT type FROM conversations WHERE id = $1`, convID).Scan(&convType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConversationNotFound
		}
		return err
	}
	if convType != "group" {
		return errors.New("can only add participants to group conversations")
	}

	// Add each user (ignore if already participant)
	for _, userID := range userIDs {
		_, err = r.db.Exec(ctx, `
			INSERT INTO conversation_participants (conversation_id, user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, convID, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetGroupParticipants returns all participants of a conversation
func (r *Repository) GetGroupParticipants(ctx context.Context, convID uuid.UUID) ([]*models.User, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM users u
		JOIN conversation_participants cp ON u.id = cp.user_id
		WHERE cp.conversation_id = $1
	`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		user := &models.User{}
		err := rows.Scan(&user.ID, &user.Email, &user.Username, &user.AvatarURL, &user.Status, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	return users, nil
}

// UpdateGroupAvatar updates the avatar URL for a group conversation
func (r *Repository) UpdateGroupAvatar(ctx context.Context, convID, userID uuid.UUID, avatarURL string) error {
	// Verify user is the owner (or owner is not set for legacy groups)
	var ownerID *uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT owner_id FROM conversations WHERE id = $1 AND type = 'group'
	`, convID).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConversationNotFound
		}
		return err
	}

	// Allow if owner_id is null (legacy) or user is the owner
	if ownerID != nil && *ownerID != userID {
		return errors.New("only the group owner can update the avatar")
	}

	// Also verify user is a participant
	var isParticipant bool
	_ = r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, userID).Scan(&isParticipant)
	if !isParticipant {
		return ErrNotParticipant
	}

	_, err = r.db.Exec(ctx, `
		UPDATE conversations SET avatar_url = $1, updated_at = NOW() WHERE id = $2
	`, avatarURL, convID)
	return err
}

// UpdateGroupName updates the name of a group conversation
func (r *Repository) UpdateGroupName(ctx context.Context, convID, userID uuid.UUID, name string) error {
	// Verify user is the owner (or owner is not set for legacy groups)
	var ownerID *uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT owner_id FROM conversations WHERE id = $1 AND type = 'group'
	`, convID).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConversationNotFound
		}
		return err
	}

	// Allow if owner_id is null (legacy) or user is the owner
	if ownerID != nil && *ownerID != userID {
		return errors.New("only the group owner can update the name")
	}

	// Also verify user is a participant
	var isParticipant bool
	_ = r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, userID).Scan(&isParticipant)
	if !isParticipant {
		return ErrNotParticipant
	}

	_, err = r.db.Exec(ctx, `
		UPDATE conversations SET name = $1, updated_at = NOW() WHERE id = $2
	`, name, convID)
	return err
}

// LeaveGroup removes a user from a group conversation
func (r *Repository) LeaveGroup(ctx context.Context, convID, userID uuid.UUID) error {
	// Verify it's a group conversation
	var convType string
	err := r.db.QueryRow(ctx, `SELECT type FROM conversations WHERE id = $1`, convID).Scan(&convType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConversationNotFound
		}
		return err
	}
	if convType != "group" {
		return errors.New("can only leave group conversations")
	}

	// Remove user from participants
	result, err := r.db.Exec(ctx, `
		DELETE FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2
	`, convID, userID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrNotParticipant
	}

	return nil
}

// GetParticipantIDs returns all participant user IDs for a conversation
func (r *Repository) GetParticipantIDs(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id FROM conversation_participants WHERE conversation_id = $1
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// DeleteMessage deletes a message if user is sender or group owner
func (r *Repository) DeleteMessage(ctx context.Context, convID, messageID, userID uuid.UUID) error {
	// Check if user is participant
	var isParticipant bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, userID).Scan(&isParticipant)
	if err != nil {
		return err
	}
	if !isParticipant {
		return ErrNotParticipant
	}

	// Get message sender and conversation owner
	var senderID uuid.UUID
	var ownerID *uuid.UUID
	err = r.db.QueryRow(ctx, `
		SELECT m.sender_id, c.owner_id
		FROM messages m
		JOIN conversations c ON m.conversation_id = c.id
		WHERE m.id = $1 AND m.conversation_id = $2
	`, messageID, convID).Scan(&senderID, &ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMessageNotFound
		}
		return err
	}

	// User can delete if they're the sender OR if they're the group owner
	canDelete := senderID == userID || (ownerID != nil && *ownerID == userID)
	if !canDelete {
		return errors.New("you can only delete your own messages")
	}

	// Delete attachments first (if any)
	_, _ = r.db.Exec(ctx, `DELETE FROM attachments WHERE message_id = $1`, messageID)

	// Delete the message
	result, err := r.db.Exec(ctx, `DELETE FROM messages WHERE id = $1`, messageID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrMessageNotFound
	}

	return nil
}

// AddReaction adds a reaction to a message
func (r *Repository) AddReaction(ctx context.Context, convID, messageID, userID uuid.UUID, emoji string) (*models.Reaction, error) {
	// Check if user is participant
	var isParticipant bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, userID).Scan(&isParticipant)
	if err != nil {
		return nil, err
	}
	if !isParticipant {
		return nil, ErrNotParticipant
	}

	// Verify message exists in this conversation
	var exists bool
	err = r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1 AND conversation_id = $2)
	`, messageID, convID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrMessageNotFound
	}

	// Insert or ignore if already exists
	reaction := &models.Reaction{}
	err = r.db.QueryRow(ctx, `
		INSERT INTO reactions (message_id, user_id, emoji)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id, user_id, emoji) DO UPDATE SET emoji = EXCLUDED.emoji
		RETURNING id, message_id, user_id, emoji, created_at
	`, messageID, userID, emoji).Scan(&reaction.ID, &reaction.MessageID, &reaction.UserID, &reaction.Emoji, &reaction.CreatedAt)
	if err != nil {
		return nil, err
	}

	// Load user info
	reaction.User = &models.User{}
	_ = r.db.QueryRow(ctx, `
		SELECT id, email, username, avatar_url, status, created_at, updated_at
		FROM users WHERE id = $1
	`, userID).Scan(
		&reaction.User.ID, &reaction.User.Email, &reaction.User.Username,
		&reaction.User.AvatarURL, &reaction.User.Status, &reaction.User.CreatedAt, &reaction.User.UpdatedAt,
	)

	return reaction, nil
}

// RemoveReaction removes a reaction from a message
func (r *Repository) RemoveReaction(ctx context.Context, convID, messageID, userID uuid.UUID, emoji string) error {
	// Check if user is participant
	var isParticipant bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2)
	`, convID, userID).Scan(&isParticipant)
	if err != nil {
		return err
	}
	if !isParticipant {
		return ErrNotParticipant
	}

	result, err := r.db.Exec(ctx, `
		DELETE FROM reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3
	`, messageID, userID, emoji)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("reaction not found")
	}

	return nil
}

// GetMessageReactions gets all reactions for a message
func (r *Repository) GetMessageReactions(ctx context.Context, messageID uuid.UUID) ([]*models.Reaction, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.message_id, r.user_id, r.emoji, r.created_at,
			   u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM reactions r
		JOIN users u ON r.user_id = u.id
		WHERE r.message_id = $1
		ORDER BY r.created_at
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reactions []*models.Reaction
	for rows.Next() {
		reaction := &models.Reaction{User: &models.User{}}
		err := rows.Scan(
			&reaction.ID, &reaction.MessageID, &reaction.UserID, &reaction.Emoji, &reaction.CreatedAt,
			&reaction.User.ID, &reaction.User.Email, &reaction.User.Username,
			&reaction.User.AvatarURL, &reaction.User.Status, &reaction.User.CreatedAt, &reaction.User.UpdatedAt,
		)
		if err != nil {
			continue
		}
		reactions = append(reactions, reaction)
	}

	return reactions, nil
}

// loadReactions loads reactions for a message
func (r *Repository) loadReactions(ctx context.Context, messageID uuid.UUID) []*models.Reaction {
	reactions, _ := r.GetMessageReactions(ctx, messageID)
	return reactions
}

// CreateCallMessage creates a call system message in a conversation
func (r *Repository) CreateCallMessage(ctx context.Context, convID, senderID uuid.UUID, content string) (*models.Message, error) {
	msg := &models.Message{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, sender_id, type, content)
		VALUES ($1, $2, 'call', $3)
		RETURNING id, conversation_id, sender_id, type, content, created_at, updated_at
	`, convID, senderID, content).Scan(
		&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Type, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Update conversation updated_at
	_, _ = r.db.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, convID)

	// Get sender info
	msg.Sender = &models.User{}
	_ = r.db.QueryRow(ctx, `
		SELECT id, email, username, avatar_url, status, created_at, updated_at
		FROM users WHERE id = $1
	`, senderID).Scan(
		&msg.Sender.ID, &msg.Sender.Email, &msg.Sender.Username, &msg.Sender.AvatarURL, &msg.Sender.Status, &msg.Sender.CreatedAt, &msg.Sender.UpdatedAt,
	)

	msg.Attachments = []*models.Attachment{}
	msg.Reactions = []*models.Reaction{}

	return msg, nil
}
