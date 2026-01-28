package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/bla-back/internal/auth"
	"github.com/user/bla-back/internal/cache"
	"github.com/user/bla-back/internal/calls"
	"github.com/user/bla-back/internal/config"
	"github.com/user/bla-back/internal/database"
	"github.com/user/bla-back/internal/friends"
	"github.com/user/bla-back/internal/handlers"
	"github.com/user/bla-back/internal/messages"
	"github.com/user/bla-back/internal/middleware"
	"github.com/user/bla-back/internal/realtime"
	"github.com/user/bla-back/internal/stickers"
	"github.com/user/bla-back/internal/storage"
)

func main() {
	cfg := config.Load()

	// Database
	db, err := database.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(context.Background()); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	log.Println("Database migrations completed")

	// Services
	tokenService := auth.NewTokenService(
		cfg.JWTSecret,
		cfg.RefreshSecret,
		cfg.AccessTokenTTL,
		cfg.RefreshTokenTTL,
	)

	// Repositories
	authRepo := auth.NewRepository(db.Pool)
	friendsRepo := friends.NewRepository(db.Pool)
	messagesRepo := messages.NewRepository(db.Pool)
	callsRepo := calls.NewRepository(db.Pool)
	stickersRepo := stickers.NewRepository(db.Pool)

	// Voice service (custom SFU)
	voiceService := calls.NewVoiceService(calls.VoiceConfig{
		Host:      cfg.VoiceHost,
		JWTSecret: cfg.VoiceJWTSecret,
	})

	// S3 Storage
	s3Storage, err := storage.NewS3Storage(storage.Config{
		Endpoint:        cfg.S3Endpoint,
		Region:          cfg.S3Region,
		Bucket:          cfg.S3Bucket,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		CDNURL:          cfg.S3CDNURL,
	})
	if err != nil {
		log.Fatalf("Failed to create S3 storage: %v", err)
	}
	log.Println("S3 storage initialized")

	// Redis Cache (optional)
	var redisCache *cache.RedisCache
	if cfg.RedisAddr != "" && cfg.RedisAddr != "disabled" {
		redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
		if err != nil {
			log.Printf("Warning: Redis not available, running without cache: %v", err)
			redisCache = nil
		} else {
			defer redisCache.Close()
			log.Println("Redis cache initialized")
		}
	} else {
		log.Println("Redis disabled, running without cache")
	}

	// Realtime data provider
	rtProvider := realtime.NewProvider(authRepo, friendsRepo, messagesRepo, callsRepo)

	// Centrifuge realtime node
	rtNode, err := realtime.NewNode(tokenService, rtProvider, friendsRepo)
	if err != nil {
		log.Fatalf("Failed to create realtime node: %v", err)
	}

	// Realtime notifier for handlers
	rtNotifier := realtime.NewNotifier(rtNode)

	// Handlers
	authHandler := handlers.NewAuthHandler(authRepo, tokenService, s3Storage)
	friendsHandler := handlers.NewFriendsHandler(friendsRepo, rtNode)
	messagesHandler := handlers.NewMessagesHandler(messagesRepo, rtNode, s3Storage)
	callsHandler := handlers.NewCallsHandler(callsRepo, voiceService, authRepo, rtNotifier, messagesRepo)
	stickersHandler := handlers.NewStickersHandler(stickersRepo, s3Storage, redisCache)

	// Router
	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("POST /api/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/auth/refresh", authHandler.Refresh)
	mux.HandleFunc("POST /api/auth/logout", authHandler.Logout)

	// Protected routes - Auth
	authMiddleware := middleware.Auth(tokenService)
	mux.Handle("GET /api/auth/me", authMiddleware(http.HandlerFunc(authHandler.Me)))
	mux.Handle("POST /api/auth/username", authMiddleware(http.HandlerFunc(authHandler.SetUsername)))
	mux.Handle("POST /api/auth/avatar", authMiddleware(http.HandlerFunc(authHandler.UploadAvatar)))

	// Protected routes - Friends
	mux.Handle("GET /api/friends", authMiddleware(http.HandlerFunc(friendsHandler.GetFriends)))
	mux.Handle("DELETE /api/friends/{id}", authMiddleware(http.HandlerFunc(friendsHandler.RemoveFriend)))

	// Friend requests
	mux.Handle("POST /api/friends/request", authMiddleware(http.HandlerFunc(friendsHandler.SendRequest)))
	mux.Handle("POST /api/friends/request/username", authMiddleware(http.HandlerFunc(friendsHandler.SendRequestByUsername)))
	mux.Handle("GET /api/friends/requests/incoming", authMiddleware(http.HandlerFunc(friendsHandler.GetIncomingRequests)))
	mux.Handle("GET /api/friends/requests/outgoing", authMiddleware(http.HandlerFunc(friendsHandler.GetOutgoingRequests)))
	mux.Handle("POST /api/friends/requests/{id}/accept", authMiddleware(http.HandlerFunc(friendsHandler.AcceptRequest)))
	mux.Handle("POST /api/friends/requests/{id}/decline", authMiddleware(http.HandlerFunc(friendsHandler.DeclineRequest)))
	mux.Handle("DELETE /api/friends/requests/{id}", authMiddleware(http.HandlerFunc(friendsHandler.CancelRequest)))

	// Blocks
	mux.Handle("GET /api/blocks", authMiddleware(http.HandlerFunc(friendsHandler.GetBlocks)))
	mux.Handle("POST /api/blocks", authMiddleware(http.HandlerFunc(friendsHandler.Block)))
	mux.Handle("DELETE /api/blocks/{id}", authMiddleware(http.HandlerFunc(friendsHandler.Unblock)))

	// Messages & Conversations
	mux.Handle("GET /api/conversations", authMiddleware(http.HandlerFunc(messagesHandler.GetConversations)))
	mux.Handle("POST /api/conversations/dm", authMiddleware(http.HandlerFunc(messagesHandler.GetOrCreateDM)))
	mux.Handle("POST /api/conversations/group", authMiddleware(http.HandlerFunc(messagesHandler.CreateGroup)))
	mux.Handle("GET /api/conversations/{id}", authMiddleware(http.HandlerFunc(messagesHandler.GetConversation)))
	mux.Handle("GET /api/conversations/{id}/messages", authMiddleware(http.HandlerFunc(messagesHandler.GetMessages)))
	mux.Handle("POST /api/conversations/{id}/messages", authMiddleware(http.HandlerFunc(messagesHandler.SendMessage)))
	mux.Handle("DELETE /api/conversations/{id}/messages/{messageId}", authMiddleware(http.HandlerFunc(messagesHandler.DeleteMessage)))
	mux.Handle("POST /api/conversations/{id}/messages/{messageId}/reactions", authMiddleware(http.HandlerFunc(messagesHandler.AddReaction)))
	mux.Handle("DELETE /api/conversations/{id}/messages/{messageId}/reactions/{emoji}", authMiddleware(http.HandlerFunc(messagesHandler.RemoveReaction)))
	mux.Handle("POST /api/conversations/{id}/participants", authMiddleware(http.HandlerFunc(messagesHandler.AddParticipants)))
	mux.Handle("POST /api/conversations/{id}/avatar", authMiddleware(http.HandlerFunc(messagesHandler.UploadGroupAvatar)))
	mux.Handle("PATCH /api/conversations/{id}", authMiddleware(http.HandlerFunc(messagesHandler.UpdateGroup)))
	mux.Handle("DELETE /api/conversations/{id}/leave", authMiddleware(http.HandlerFunc(messagesHandler.LeaveGroup)))

	// Attachments
	mux.Handle("POST /api/attachments", authMiddleware(http.HandlerFunc(messagesHandler.UploadAttachment)))

	// Calls
	mux.Handle("POST /api/calls/start", authMiddleware(http.HandlerFunc(callsHandler.StartCall)))
	mux.Handle("POST /api/calls/join", authMiddleware(http.HandlerFunc(callsHandler.JoinCall)))
	mux.Handle("POST /api/calls/leave", authMiddleware(http.HandlerFunc(callsHandler.LeaveCall)))
	mux.Handle("GET /api/conversations/{id}/call", authMiddleware(http.HandlerFunc(callsHandler.GetActiveCall)))

	// Stickers
	mux.Handle("GET /api/stickers", authMiddleware(http.HandlerFunc(stickersHandler.GetPacks)))
	mux.Handle("GET /api/stickers/{id}", authMiddleware(http.HandlerFunc(stickersHandler.GetPack)))
	mux.HandleFunc("GET /api/stickers/file/{stickerId}", stickersHandler.ProxySticker) // Public, no auth for caching
	mux.Handle("POST /api/stickers", authMiddleware(http.HandlerFunc(stickersHandler.CreatePack)))
	mux.Handle("POST /api/stickers/{id}/stickers", authMiddleware(http.HandlerFunc(stickersHandler.UploadSticker)))
	mux.Handle("POST /api/stickers/{id}/add", authMiddleware(http.HandlerFunc(stickersHandler.AddPackToCollection)))
	mux.Handle("DELETE /api/stickers/{id}/remove", authMiddleware(http.HandlerFunc(stickersHandler.RemovePackFromCollection)))
	mux.Handle("DELETE /api/stickers/{id}", authMiddleware(http.HandlerFunc(stickersHandler.DeletePack)))

	// Centrifuge WebSocket endpoint
	mux.Handle("GET /api/ws", rtNode.WebsocketHandler())

	// Apply CORS
	handler := middleware.CORS(mux)

	// Server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := rtNode.Shutdown(ctx); err != nil {
			log.Printf("Centrifuge shutdown error: %v", err)
		}

		if err := server.Shutdown(ctx); err != nil {
			log.Fatalf("Server shutdown failed: %v", err)
		}
	}()

	log.Printf("Server starting on port %s", cfg.Port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}

	log.Println("Server stopped")
}
