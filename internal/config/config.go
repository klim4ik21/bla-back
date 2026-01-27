package config

import (
	"os"
	"time"
)

type Config struct {
	Port            string
	DatabaseURL     string
	JWTSecret       string
	RefreshSecret   string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// S3 Storage
	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3CDNURL          string

	// LiveKit
	LiveKitHost      string
	LiveKitAPIKey    string
	LiveKitAPISecret string

	// Redis
	RedisAddr string
}

func Load() *Config {
	return &Config{
		Port:            getEnv("PORT", "8080"),
		DatabaseURL:     getEnv("DATABASE_URL", "postgresql://bla:bla@localhost:5432/bla"),
		JWTSecret:       getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production"),
		RefreshSecret:   getEnv("REFRESH_SECRET", "your-super-secret-refresh-key-change-in-production"),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,

		// S3 Storage - Timeweb
		S3Endpoint:        getEnv("S3_ENDPOINT", "https://s3.twcstorage.ru"),
		S3Region:          getEnv("S3_REGION", "ru-1"),
		S3Bucket:          getEnv("S3_BUCKET", "f5d9c802-spb1"),
		S3AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", "MYRENGLV1CE5YWB4G8BF"),
		S3SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", "KphWppiBgaPUMWZp1xdaXc7H5CcNxNBz22BDeHJO"),
		S3CDNURL:          getEnv("S3_CDN_URL", "https://cdn.richislav.com/f5d9c802-spb1"),

		// LiveKit
		LiveKitHost:      getEnv("LIVEKIT_HOST", "ws://45.144.221.4:7880"),
		LiveKitAPIKey:    getEnv("LIVEKIT_API_KEY", "devkey"),
		LiveKitAPISecret: getEnv("LIVEKIT_API_SECRET", "secretsecretsecretsecretsecretsecret"),

		// Redis
		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
