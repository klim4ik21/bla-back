package friends

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/bla-back/internal/models"
)

var (
	ErrRequestNotFound     = errors.New("friend request not found")
	ErrRequestAlreadyExists = errors.New("friend request already exists")
	ErrAlreadyFriends      = errors.New("already friends")
	ErrCannotAddSelf       = errors.New("cannot send friend request to yourself")
	ErrUserBlocked         = errors.New("user is blocked")
	ErrBlockNotFound       = errors.New("block not found")
	ErrAlreadyBlocked      = errors.New("user already blocked")
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// SendRequest creates a new friend request
func (r *Repository) SendRequest(ctx context.Context, fromUserID, toUserID uuid.UUID) (*models.FriendRequest, error) {
	if fromUserID == toUserID {
		return nil, ErrCannotAddSelf
	}

	// Check if blocked
	blocked, err := r.IsBlocked(ctx, fromUserID, toUserID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrUserBlocked
	}

	// Check if already friends
	friends, err := r.AreFriends(ctx, fromUserID, toUserID)
	if err != nil {
		return nil, err
	}
	if friends {
		return nil, ErrAlreadyFriends
	}

	// Check for existing pending request (in either direction)
	existing, err := r.GetPendingRequest(ctx, fromUserID, toUserID)
	if err != nil && !errors.Is(err, ErrRequestNotFound) {
		return nil, err
	}
	if existing != nil {
		// If other user already sent us a request, auto-accept
		if existing.FromUserID == toUserID {
			return r.AcceptRequest(ctx, toUserID, existing.ID)
		}
		return nil, ErrRequestAlreadyExists
	}

	req := &models.FriendRequest{}
	err = r.db.QueryRow(ctx, `
		INSERT INTO friend_requests (from_user_id, to_user_id, status)
		VALUES ($1, $2, 'pending')
		RETURNING id, from_user_id, to_user_id, status, created_at, updated_at
	`, fromUserID, toUserID).Scan(
		&req.ID, &req.FromUserID, &req.ToUserID, &req.Status, &req.CreatedAt, &req.UpdatedAt,
	)

	return req, err
}

// GetPendingRequest finds a pending request between two users (in either direction)
func (r *Repository) GetPendingRequest(ctx context.Context, userA, userB uuid.UUID) (*models.FriendRequest, error) {
	req := &models.FriendRequest{}
	err := r.db.QueryRow(ctx, `
		SELECT id, from_user_id, to_user_id, status, created_at, updated_at
		FROM friend_requests
		WHERE status = 'pending'
		AND ((from_user_id = $1 AND to_user_id = $2) OR (from_user_id = $2 AND to_user_id = $1))
	`, userA, userB).Scan(
		&req.ID, &req.FromUserID, &req.ToUserID, &req.Status, &req.CreatedAt, &req.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return req, err
}

// GetRequestByID gets a friend request by ID
func (r *Repository) GetRequestByID(ctx context.Context, requestID uuid.UUID) (*models.FriendRequest, error) {
	req := &models.FriendRequest{}
	err := r.db.QueryRow(ctx, `
		SELECT id, from_user_id, to_user_id, status, created_at, updated_at
		FROM friend_requests WHERE id = $1
	`, requestID).Scan(
		&req.ID, &req.FromUserID, &req.ToUserID, &req.Status, &req.CreatedAt, &req.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return req, err
}

// AcceptRequest accepts a friend request
func (r *Repository) AcceptRequest(ctx context.Context, userID, requestID uuid.UUID) (*models.FriendRequest, error) {
	req := &models.FriendRequest{}
	err := r.db.QueryRow(ctx, `
		UPDATE friend_requests
		SET status = 'accepted', updated_at = NOW()
		WHERE id = $1 AND to_user_id = $2 AND status = 'pending'
		RETURNING id, from_user_id, to_user_id, status, created_at, updated_at
	`, requestID, userID).Scan(
		&req.ID, &req.FromUserID, &req.ToUserID, &req.Status, &req.CreatedAt, &req.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return req, err
}

// DeclineRequest declines a friend request
func (r *Repository) DeclineRequest(ctx context.Context, userID, requestID uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		UPDATE friend_requests
		SET status = 'declined', updated_at = NOW()
		WHERE id = $1 AND to_user_id = $2 AND status = 'pending'
	`, requestID, userID)

	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrRequestNotFound
	}
	return nil
}

// CancelRequest cancels an outgoing friend request
func (r *Repository) CancelRequest(ctx context.Context, userID, requestID uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		DELETE FROM friend_requests
		WHERE id = $1 AND from_user_id = $2 AND status = 'pending'
	`, requestID, userID)

	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrRequestNotFound
	}
	return nil
}

// RemoveFriend removes a friendship
func (r *Repository) RemoveFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		DELETE FROM friend_requests
		WHERE status = 'accepted'
		AND ((from_user_id = $1 AND to_user_id = $2) OR (from_user_id = $2 AND to_user_id = $1))
	`, userID, friendID)

	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrRequestNotFound
	}
	return nil
}

// GetFriends returns all friends of a user
func (r *Repository) GetFriends(ctx context.Context, userID uuid.UUID) ([]*models.FriendWithUser, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			fr.id,
			fr.updated_at,
			u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM friend_requests fr
		JOIN users u ON (
			CASE
				WHEN fr.from_user_id = $1 THEN fr.to_user_id = u.id
				ELSE fr.from_user_id = u.id
			END
		)
		WHERE fr.status = 'accepted'
		AND (fr.from_user_id = $1 OR fr.to_user_id = $1)
		ORDER BY u.username
	`, userID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var friends []*models.FriendWithUser
	for rows.Next() {
		f := &models.FriendWithUser{User: &models.User{}}
		err := rows.Scan(
			&f.FriendshipID,
			&f.Since,
			&f.User.ID, &f.User.Email, &f.User.Username, &f.User.AvatarURL, &f.User.Status, &f.User.CreatedAt, &f.User.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		friends = append(friends, f)
	}

	return friends, rows.Err()
}

// GetIncomingRequests returns pending requests sent to the user
func (r *Repository) GetIncomingRequests(ctx context.Context, userID uuid.UUID) ([]*models.FriendRequestWithUser, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			fr.id, fr.status, fr.created_at,
			u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM friend_requests fr
		JOIN users u ON fr.from_user_id = u.id
		WHERE fr.to_user_id = $1 AND fr.status = 'pending'
		ORDER BY fr.created_at DESC
	`, userID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []*models.FriendRequestWithUser
	for rows.Next() {
		r := &models.FriendRequestWithUser{User: &models.User{}}
		err := rows.Scan(
			&r.ID, &r.Status, &r.CreatedAt,
			&r.User.ID, &r.User.Email, &r.User.Username, &r.User.AvatarURL, &r.User.Status, &r.User.CreatedAt, &r.User.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		requests = append(requests, r)
	}

	return requests, rows.Err()
}

// GetOutgoingRequests returns pending requests sent by the user
func (r *Repository) GetOutgoingRequests(ctx context.Context, userID uuid.UUID) ([]*models.FriendRequestWithUser, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			fr.id, fr.status, fr.created_at,
			u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM friend_requests fr
		JOIN users u ON fr.to_user_id = u.id
		WHERE fr.from_user_id = $1 AND fr.status = 'pending'
		ORDER BY fr.created_at DESC
	`, userID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []*models.FriendRequestWithUser
	for rows.Next() {
		r := &models.FriendRequestWithUser{User: &models.User{}}
		err := rows.Scan(
			&r.ID, &r.Status, &r.CreatedAt,
			&r.User.ID, &r.User.Email, &r.User.Username, &r.User.AvatarURL, &r.User.Status, &r.User.CreatedAt, &r.User.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		requests = append(requests, r)
	}

	return requests, rows.Err()
}

// AreFriends checks if two users are friends
func (r *Repository) AreFriends(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM friend_requests
			WHERE status = 'accepted'
			AND ((from_user_id = $1 AND to_user_id = $2) OR (from_user_id = $2 AND to_user_id = $1))
		)
	`, userA, userB).Scan(&exists)

	return exists, err
}

// Block blocks a user
func (r *Repository) Block(ctx context.Context, blockerID, blockedID uuid.UUID) (*models.Block, error) {
	if blockerID == blockedID {
		return nil, ErrCannotAddSelf
	}

	// Remove any existing friendship
	_, _ = r.db.Exec(ctx, `
		DELETE FROM friend_requests
		WHERE (from_user_id = $1 AND to_user_id = $2) OR (from_user_id = $2 AND to_user_id = $1)
	`, blockerID, blockedID)

	block := &models.Block{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO blocks (blocker_id, blocked_id)
		VALUES ($1, $2)
		ON CONFLICT (blocker_id, blocked_id) DO UPDATE SET blocker_id = EXCLUDED.blocker_id
		RETURNING id, blocker_id, blocked_id, created_at
	`, blockerID, blockedID).Scan(
		&block.ID, &block.BlockerID, &block.BlockedID, &block.CreatedAt,
	)

	return block, err
}

// Unblock unblocks a user
func (r *Repository) Unblock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2
	`, blockerID, blockedID)

	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrBlockNotFound
	}
	return nil
}

// GetBlocks returns all users blocked by this user
func (r *Repository) GetBlocks(ctx context.Context, userID uuid.UUID) ([]*models.BlockWithUser, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			b.id, b.created_at,
			u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM blocks b
		JOIN users u ON b.blocked_id = u.id
		WHERE b.blocker_id = $1
		ORDER BY b.created_at DESC
	`, userID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var blocks []*models.BlockWithUser
	for rows.Next() {
		b := &models.BlockWithUser{User: &models.User{}}
		err := rows.Scan(
			&b.ID, &b.CreatedAt,
			&b.User.ID, &b.User.Email, &b.User.Username, &b.User.AvatarURL, &b.User.Status, &b.User.CreatedAt, &b.User.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}

	return blocks, rows.Err()
}

// IsBlocked checks if either user has blocked the other
func (r *Repository) IsBlocked(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2) OR (blocker_id = $2 AND blocked_id = $1)
		)
	`, userA, userB).Scan(&exists)

	return exists, err
}

// GetUserByUsername finds a user by username
func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	user := &models.User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, username, avatar_url, status, created_at, updated_at
		FROM users WHERE username = $1
	`, username).Scan(
		&user.ID, &user.Email, &user.Username, &user.AvatarURL, &user.Status, &user.CreatedAt, &user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("user not found")
	}
	return user, err
}

// GetRequest gets a friend request by ID (alias for GetRequestByID)
func (r *Repository) GetRequest(ctx context.Context, requestID uuid.UUID) (*models.FriendRequest, error) {
	return r.GetRequestByID(ctx, requestID)
}

// GetRequestWithUser gets a friend request with user info for the receiver
func (r *Repository) GetRequestWithUser(ctx context.Context, requestID, receiverID uuid.UUID) (*models.FriendRequestWithUser, error) {
	req := &models.FriendRequestWithUser{User: &models.User{}}
	err := r.db.QueryRow(ctx, `
		SELECT
			fr.id, fr.status, fr.created_at,
			u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM friend_requests fr
		JOIN users u ON fr.from_user_id = u.id
		WHERE fr.id = $1 AND fr.to_user_id = $2
	`, requestID, receiverID).Scan(
		&req.ID, &req.Status, &req.CreatedAt,
		&req.User.ID, &req.User.Email, &req.User.Username, &req.User.AvatarURL, &req.User.Status, &req.User.CreatedAt, &req.User.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return req, err
}

// GetFriendIDs returns just the user IDs of all friends
func (r *Repository) GetFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			CASE
				WHEN from_user_id = $1 THEN to_user_id
				ELSE from_user_id
			END as friend_id
		FROM friend_requests
		WHERE status = 'accepted'
		AND (from_user_id = $1 OR to_user_id = $1)
	`, userID)

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

	return ids, rows.Err()
}

// GetFriendByUserID gets a friend with user info
func (r *Repository) GetFriendByUserID(ctx context.Context, userID, friendUserID uuid.UUID) (*models.FriendWithUser, error) {
	f := &models.FriendWithUser{User: &models.User{}}
	err := r.db.QueryRow(ctx, `
		SELECT
			fr.id,
			fr.updated_at,
			u.id, u.email, u.username, u.avatar_url, u.status, u.created_at, u.updated_at
		FROM friend_requests fr
		JOIN users u ON u.id = $2
		WHERE fr.status = 'accepted'
		AND ((fr.from_user_id = $1 AND fr.to_user_id = $2) OR (fr.from_user_id = $2 AND fr.to_user_id = $1))
	`, userID, friendUserID).Scan(
		&f.FriendshipID,
		&f.Since,
		&f.User.ID, &f.User.Email, &f.User.Username, &f.User.AvatarURL, &f.User.Status, &f.User.CreatedAt, &f.User.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	return f, err
}
