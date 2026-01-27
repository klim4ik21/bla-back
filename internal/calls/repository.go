package calls

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Call struct {
	ID             uuid.UUID    `json:"id"`
	ConversationID uuid.UUID    `json:"conversation_id"`
	StartedBy      uuid.UUID    `json:"started_by"`
	StartedAt      time.Time    `json:"started_at"`
	EndedAt        *time.Time   `json:"ended_at"`
	Participants   []Participant `json:"participants,omitempty"`
}

type Participant struct {
	UserID   uuid.UUID  `json:"user_id"`
	JoinedAt time.Time  `json:"joined_at"`
	LeftAt   *time.Time `json:"left_at,omitempty"`
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetActiveCallForConversation returns the active call in a conversation (if any)
func (r *Repository) GetActiveCallForConversation(ctx context.Context, conversationID uuid.UUID) (*Call, error) {
	call := &Call{}
	err := r.db.QueryRow(ctx, `
		SELECT id, conversation_id, started_by, started_at, ended_at
		FROM calls
		WHERE conversation_id = $1 AND ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1
	`, conversationID).Scan(
		&call.ID, &call.ConversationID, &call.StartedBy, &call.StartedAt, &call.EndedAt,
	)
	if err != nil {
		return nil, err
	}
	return call, nil
}

// GetCallWithParticipants returns a call with its active participants
func (r *Repository) GetCallWithParticipants(ctx context.Context, callID uuid.UUID) (*Call, error) {
	call := &Call{}
	err := r.db.QueryRow(ctx, `
		SELECT id, conversation_id, started_by, started_at, ended_at
		FROM calls WHERE id = $1
	`, callID).Scan(
		&call.ID, &call.ConversationID, &call.StartedBy, &call.StartedAt, &call.EndedAt,
	)
	if err != nil {
		return nil, err
	}

	// Get active participants
	rows, err := r.db.Query(ctx, `
		SELECT user_id, joined_at, left_at
		FROM call_participants
		WHERE call_id = $1 AND left_at IS NULL
		ORDER BY joined_at
	`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.UserID, &p.JoinedAt, &p.LeftAt); err != nil {
			return nil, err
		}
		call.Participants = append(call.Participants, p)
	}

	return call, nil
}

// StartCall creates a new call in a conversation and adds the starter as first participant
func (r *Repository) StartCall(ctx context.Context, conversationID, userID uuid.UUID) (*Call, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	call := &Call{
		ID:             uuid.New(),
		ConversationID: conversationID,
		StartedBy:      userID,
		StartedAt:      time.Now(),
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO calls (id, conversation_id, started_by, started_at)
		VALUES ($1, $2, $3, $4)
	`, call.ID, call.ConversationID, call.StartedBy, call.StartedAt)
	if err != nil {
		return nil, err
	}

	// Add starter as first participant
	_, err = tx.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, joined_at)
		VALUES ($1, $2, $3)
	`, call.ID, userID, call.StartedAt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	call.Participants = []Participant{{UserID: userID, JoinedAt: call.StartedAt}}
	return call, nil
}

// JoinCall adds a user to an existing call
func (r *Repository) JoinCall(ctx context.Context, callID, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, joined_at)
		VALUES ($1, $2, $3)
	`, callID, userID, time.Now())
	return err
}

// LeaveCall marks a user as left from the call
func (r *Repository) LeaveCall(ctx context.Context, callID, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE call_participants
		SET left_at = $1
		WHERE call_id = $2 AND user_id = $3 AND left_at IS NULL
	`, time.Now(), callID, userID)
	return err
}

// EndCall marks the call as ended
func (r *Repository) EndCall(ctx context.Context, callID uuid.UUID) error {
	now := time.Now()

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Mark all participants as left
	_, err = tx.Exec(ctx, `
		UPDATE call_participants
		SET left_at = $1
		WHERE call_id = $2 AND left_at IS NULL
	`, now, callID)
	if err != nil {
		return err
	}

	// End the call
	_, err = tx.Exec(ctx, `
		UPDATE calls SET ended_at = $1 WHERE id = $2
	`, now, callID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetActiveParticipantCount returns how many users are currently in the call
func (r *Repository) GetActiveParticipantCount(ctx context.Context, callID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM call_participants
		WHERE call_id = $1 AND left_at IS NULL
	`, callID).Scan(&count)
	return count, err
}

// IsUserInCall checks if a user is currently in any active call
func (r *Repository) IsUserInCall(ctx context.Context, userID uuid.UUID) (*Call, error) {
	call := &Call{}
	err := r.db.QueryRow(ctx, `
		SELECT c.id, c.conversation_id, c.started_by, c.started_at, c.ended_at
		FROM calls c
		JOIN call_participants cp ON c.id = cp.call_id
		WHERE cp.user_id = $1 AND cp.left_at IS NULL AND c.ended_at IS NULL
		LIMIT 1
	`, userID).Scan(
		&call.ID, &call.ConversationID, &call.StartedBy, &call.StartedAt, &call.EndedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return call, nil
}

// GetActiveParticipants returns list of user IDs currently in the call
func (r *Repository) GetActiveParticipants(ctx context.Context, callID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id FROM call_participants
		WHERE call_id = $1 AND left_at IS NULL
	`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []uuid.UUID
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		participants = append(participants, userID)
	}
	return participants, nil
}

// GetActiveCallsForConversations returns all active calls for given conversation IDs
func (r *Repository) GetActiveCallsForConversations(ctx context.Context, conversationIDs []uuid.UUID) ([]*Call, error) {
	if len(conversationIDs) == 0 {
		return []*Call{}, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, conversation_id, started_by, started_at, ended_at
		FROM calls
		WHERE conversation_id = ANY($1) AND ended_at IS NULL
	`, conversationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var calls []*Call
	for rows.Next() {
		call := &Call{}
		if err := rows.Scan(&call.ID, &call.ConversationID, &call.StartedBy, &call.StartedAt, &call.EndedAt); err != nil {
			return nil, err
		}

		// Get participants for this call
		participantRows, err := r.db.Query(ctx, `
			SELECT user_id, joined_at, left_at
			FROM call_participants
			WHERE call_id = $1 AND left_at IS NULL
		`, call.ID)
		if err != nil {
			return nil, err
		}

		for participantRows.Next() {
			var p Participant
			if err := participantRows.Scan(&p.UserID, &p.JoinedAt, &p.LeftAt); err != nil {
				participantRows.Close()
				return nil, err
			}
			call.Participants = append(call.Participants, p)
		}
		participantRows.Close()

		calls = append(calls, call)
	}

	return calls, nil
}
