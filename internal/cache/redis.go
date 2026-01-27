package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(addr string) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &RedisCache{client: client}, nil
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}

// Generic cache methods

func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	return c.client.Get(ctx, key).Bytes()
}

func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// JSON helpers

func (c *RedisCache) GetJSON(ctx context.Context, key string, dest interface{}) error {
	data, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func (c *RedisCache) SetJSON(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, data, ttl)
}

// Sticker cache keys
const (
	StickerKeyPrefix     = "sticker:"
	StickerPackKeyPrefix = "sticker_pack:"
	StickerPacksTTL      = 5 * time.Minute
	StickerFileTTL       = 1 * time.Hour
)

func StickerKey(id string) string {
	return StickerKeyPrefix + id
}

func StickerPackKey(id string) string {
	return StickerPackKeyPrefix + id
}

// User online status
const (
	UserOnlineKeyPrefix = "user:online:"
	UserOnlineTTL       = 5 * time.Minute
)

func UserOnlineKey(userID string) string {
	return UserOnlineKeyPrefix + userID
}

func (c *RedisCache) SetUserOnline(ctx context.Context, userID string) error {
	return c.client.Set(ctx, UserOnlineKey(userID), "1", UserOnlineTTL).Err()
}

func (c *RedisCache) IsUserOnline(ctx context.Context, userID string) (bool, error) {
	exists, err := c.client.Exists(ctx, UserOnlineKey(userID)).Result()
	return exists > 0, err
}

func (c *RedisCache) GetOnlineUsers(ctx context.Context, userIDs []string) (map[string]bool, error) {
	result := make(map[string]bool)
	if len(userIDs) == 0 {
		return result, nil
	}

	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = UserOnlineKey(id)
	}

	vals, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	for i, id := range userIDs {
		result[id] = vals[i] != nil
	}
	return result, nil
}

// Rate limiting
func (c *RedisCache) CheckRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	current, err := c.client.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}

	if current == 1 {
		c.client.Expire(ctx, key, window)
	}

	return current <= int64(limit), nil
}
