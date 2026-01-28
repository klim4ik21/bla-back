package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}

func (db *DB) Migrate(ctx context.Context) error {
	schema := `
		CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			username VARCHAR(32) UNIQUE,
			avatar_url TEXT,
			status VARCHAR(20) DEFAULT 'offline',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS refresh_tokens (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS friend_requests (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			from_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			to_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(from_user_id, to_user_id),
			CHECK (from_user_id != to_user_id)
		);

		CREATE TABLE IF NOT EXISTS blocks (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			blocker_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			blocked_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(blocker_id, blocked_id),
			CHECK (blocker_id != blocked_id)
		);

		CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
		CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token ON refresh_tokens(token);
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);
		CREATE INDEX IF NOT EXISTS idx_friend_requests_from ON friend_requests(from_user_id);
		CREATE INDEX IF NOT EXISTS idx_friend_requests_to ON friend_requests(to_user_id);
		CREATE INDEX IF NOT EXISTS idx_friend_requests_status ON friend_requests(status);
		CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON blocks(blocker_id);
		CREATE INDEX IF NOT EXISTS idx_blocks_blocked ON blocks(blocked_id);

		CREATE TABLE IF NOT EXISTS conversations (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			type VARCHAR(20) NOT NULL DEFAULT 'dm',
			name VARCHAR(100),
			avatar_url TEXT,
			owner_id UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS conversation_participants (
			conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			joined_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			PRIMARY KEY (conversation_id, user_id)
		);

		CREATE TABLE IF NOT EXISTS messages (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			content TEXT NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_conversation_participants_user ON conversation_participants(user_id);
		CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
		CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_messages_conv_created ON messages(conversation_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS attachments (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			message_id UUID REFERENCES messages(id) ON DELETE CASCADE,
			uploader_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type VARCHAR(20) NOT NULL DEFAULT 'image',
			url TEXT NOT NULL,
			filename VARCHAR(255) NOT NULL,
			size BIGINT NOT NULL DEFAULT 0,
			width INT,
			height INT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_attachments_message ON attachments(message_id);
		CREATE INDEX IF NOT EXISTS idx_attachments_uploader ON attachments(uploader_id);

		ALTER TABLE messages ALTER COLUMN content DROP NOT NULL;

		-- Add avatar_url and owner_id to conversations if not exists
		DO $$ BEGIN
			ALTER TABLE conversations ADD COLUMN IF NOT EXISTS avatar_url TEXT;
			ALTER TABLE conversations ADD COLUMN IF NOT EXISTS owner_id UUID REFERENCES users(id) ON DELETE SET NULL;
		EXCEPTION WHEN others THEN NULL;
		END $$;

		-- Calls table for voice/video calls (Discord-style)
		CREATE TABLE IF NOT EXISTS calls (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			started_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			ended_at TIMESTAMP WITH TIME ZONE,
			UNIQUE(conversation_id, ended_at) -- only one active call per conversation
		);

		CREATE TABLE IF NOT EXISTS call_participants (
			call_id UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			joined_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			left_at TIMESTAMP WITH TIME ZONE,
			PRIMARY KEY (call_id, user_id, joined_at)
		);

		CREATE INDEX IF NOT EXISTS idx_calls_conversation ON calls(conversation_id);
		CREATE INDEX IF NOT EXISTS idx_calls_ended ON calls(ended_at);
		CREATE INDEX IF NOT EXISTS idx_call_participants_call ON call_participants(call_id);
		CREATE INDEX IF NOT EXISTS idx_call_participants_user ON call_participants(user_id);

		-- Drop old columns if exist
		ALTER TABLE calls DROP COLUMN IF EXISTS caller_id;
		ALTER TABLE calls DROP COLUMN IF EXISTS receiver_id;
		ALTER TABLE calls DROP COLUMN IF EXISTS status;

		-- Reactions table
		CREATE TABLE IF NOT EXISTS reactions (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			emoji VARCHAR(32) NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(message_id, user_id, emoji)
		);

		CREATE INDEX IF NOT EXISTS idx_reactions_message ON reactions(message_id);
		CREATE INDEX IF NOT EXISTS idx_reactions_user ON reactions(user_id);

		-- Sticker packs
		CREATE TABLE IF NOT EXISTS sticker_packs (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(64) NOT NULL,
			description VARCHAR(256),
			cover_url TEXT,
			is_official BOOLEAN DEFAULT FALSE,
			creator_id UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		-- Stickers
		CREATE TABLE IF NOT EXISTS stickers (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			pack_id UUID NOT NULL REFERENCES sticker_packs(id) ON DELETE CASCADE,
			emoji VARCHAR(32),
			file_url TEXT NOT NULL,
			file_type VARCHAR(10) NOT NULL DEFAULT 'tgs',
			width INT DEFAULT 512,
			height INT DEFAULT 512,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		-- User's saved sticker packs
		CREATE TABLE IF NOT EXISTS user_sticker_packs (
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			pack_id UUID NOT NULL REFERENCES sticker_packs(id) ON DELETE CASCADE,
			added_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			sort_order INT DEFAULT 0,
			PRIMARY KEY (user_id, pack_id)
		);

		CREATE INDEX IF NOT EXISTS idx_stickers_pack ON stickers(pack_id);
		CREATE INDEX IF NOT EXISTS idx_user_sticker_packs_user ON user_sticker_packs(user_id);

		-- Add type column to messages for call messages
		DO $$ BEGIN
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS type VARCHAR(20) DEFAULT 'text';
		EXCEPTION WHEN others THEN NULL;
		END $$;

		CREATE INDEX IF NOT EXISTS idx_messages_type ON messages(type);
	`

	_, err := db.Pool.Exec(ctx, schema)
	return err
}
