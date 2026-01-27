package realtime

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"
	"github.com/google/uuid"
	"github.com/user/bla-back/internal/auth"
	"github.com/user/bla-back/internal/models"
)

// DataProvider loads initial state for a user
type DataProvider interface {
	GetReadyState(ctx context.Context, userID uuid.UUID) (*models.ReadyEvent, error)
}

// FriendsProvider provides friend relationships
type FriendsProvider interface {
	GetFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
}

type Node struct {
	node            *centrifuge.Node
	tokenService    *auth.TokenService
	dataProvider    DataProvider
	friendsProvider FriendsProvider

	// Track online users
	onlineUsers   map[uuid.UUID]int // userID -> connection count
	onlineUsersMu sync.RWMutex
}

func NewNode(tokenService *auth.TokenService, dataProvider DataProvider, friendsProvider FriendsProvider) (*Node, error) {
	node, err := centrifuge.New(centrifuge.Config{
		LogLevel:   centrifuge.LogLevelInfo,
		LogHandler: func(e centrifuge.LogEntry) { log.Printf("[centrifuge] %s: %v", e.Message, e.Fields) },
	})
	if err != nil {
		return nil, err
	}

	n := &Node{
		node:            node,
		tokenService:    tokenService,
		dataProvider:    dataProvider,
		friendsProvider: friendsProvider,
		onlineUsers:     make(map[uuid.UUID]int),
	}

	// Auth via JWT in connect request
	node.OnConnecting(func(ctx context.Context, e centrifuge.ConnectEvent) (centrifuge.ConnectReply, error) {
		token := e.Token
		if token == "" {
			return centrifuge.ConnectReply{}, centrifuge.DisconnectInvalidToken
		}

		claims, err := tokenService.ValidateAccessToken(token)
		if err != nil {
			return centrifuge.ConnectReply{}, centrifuge.DisconnectInvalidToken
		}

		return centrifuge.ConnectReply{
			Credentials: &centrifuge.Credentials{
				UserID: claims.UserID.String(),
			},
		}, nil
	})

	node.OnConnect(func(client *centrifuge.Client) {
		log.Printf("Client connected: %s (user: %s)", client.ID(), client.UserID())

		userID, err := uuid.Parse(client.UserID())
		if err != nil {
			return
		}

		// Track connection and notify friends if first connection
		wasOffline := n.addOnlineUser(userID)
		if wasOffline {
			go n.notifyPresenceChange(userID, "online")
		}

		client.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
			expectedChannel := "user:" + client.UserID()
			if e.Channel != expectedChannel {
				cb(centrifuge.SubscribeReply{}, centrifuge.ErrorPermissionDenied)
				return
			}

			// Load and send READY event with initial state
			readyState, err := n.dataProvider.GetReadyState(context.Background(), userID)
			if err != nil {
				log.Printf("Failed to get ready state for user %s: %v", userID, err)
				cb(centrifuge.SubscribeReply{}, centrifuge.ErrorInternal)
				return
			}

			// Enrich friends with current online status
			for _, friend := range readyState.Friends {
				if n.IsOnline(friend.User.ID) {
					friend.User.Status = "online"
				} else {
					friend.User.Status = "offline"
				}
			}

			// Enrich conversation participants with current online status
			for _, conv := range readyState.Conversations {
				for _, participant := range conv.Participants {
					if n.IsOnline(participant.ID) {
						participant.Status = "online"
					} else {
						participant.Status = "offline"
					}
				}
			}

			// Send READY event after subscription
			go func() {
				time.Sleep(10 * time.Millisecond) // Small delay to ensure subscription is complete
				if err := n.PublishToUser(userID, "READY", readyState); err != nil {
					log.Printf("Failed to send READY to user %s: %v", userID, err)
				}
			}()

			cb(centrifuge.SubscribeReply{}, nil)
		})

		client.OnDisconnect(func(e centrifuge.DisconnectEvent) {
			log.Printf("Client disconnected: %s (reason: %s)", client.ID(), e.Reason)

			// Remove connection and notify friends if last connection
			wentOffline := n.removeOnlineUser(userID)
			if wentOffline {
				go n.notifyPresenceChange(userID, "offline")
			}
		})
	})

	if err := node.Run(); err != nil {
		return nil, err
	}

	return n, nil
}

// addOnlineUser adds a user connection, returns true if this is first connection (was offline)
func (n *Node) addOnlineUser(userID uuid.UUID) bool {
	n.onlineUsersMu.Lock()
	defer n.onlineUsersMu.Unlock()

	wasOffline := n.onlineUsers[userID] == 0
	n.onlineUsers[userID]++
	return wasOffline
}

// removeOnlineUser removes a user connection, returns true if no more connections (went offline)
func (n *Node) removeOnlineUser(userID uuid.UUID) bool {
	n.onlineUsersMu.Lock()
	defer n.onlineUsersMu.Unlock()

	n.onlineUsers[userID]--
	if n.onlineUsers[userID] <= 0 {
		delete(n.onlineUsers, userID)
		return true
	}
	return false
}

// IsOnline checks if a user is currently online
func (n *Node) IsOnline(userID uuid.UUID) bool {
	n.onlineUsersMu.RLock()
	defer n.onlineUsersMu.RUnlock()
	return n.onlineUsers[userID] > 0
}

// notifyPresenceChange notifies all friends about a user's status change
func (n *Node) notifyPresenceChange(userID uuid.UUID, status string) {
	friendIDs, err := n.friendsProvider.GetFriendIDs(context.Background(), userID)
	if err != nil {
		log.Printf("Failed to get friend IDs for presence update: %v", err)
		return
	}

	event := &models.PresenceUpdateEvent{
		UserID: userID,
		Status: status,
	}

	n.PublishToUsers(friendIDs, "PRESENCE_UPDATE", event)
}

func (n *Node) Shutdown(ctx context.Context) error {
	return n.node.Shutdown(ctx)
}

func (n *Node) WebsocketHandler() http.Handler {
	wsHandler := centrifuge.NewWebsocketHandler(n.node, centrifuge.WebsocketConfig{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	})
	return wsHandler
}

func (n *Node) PublishToUser(userID uuid.UUID, eventType string, data interface{}) error {
	channel := "user:" + userID.String()

	payload, err := json.Marshal(map[string]interface{}{
		"type": eventType,
		"data": data,
	})
	if err != nil {
		return err
	}

	_, err = n.node.Publish(channel, payload)
	return err
}

func (n *Node) PublishToUsers(userIDs []uuid.UUID, eventType string, data interface{}) {
	for _, userID := range userIDs {
		if err := n.PublishToUser(userID, eventType, data); err != nil {
			log.Printf("Failed to publish to user %s: %v", userID, err)
		}
	}
}
