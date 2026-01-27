package realtime

import (
	"context"

	"github.com/google/uuid"
	"github.com/user/bla-back/internal/auth"
	"github.com/user/bla-back/internal/calls"
	"github.com/user/bla-back/internal/friends"
	"github.com/user/bla-back/internal/messages"
	"github.com/user/bla-back/internal/models"
)

// Provider implements DataProvider interface
type Provider struct {
	authRepo     *auth.Repository
	friendsRepo  *friends.Repository
	messagesRepo *messages.Repository
	callsRepo    *calls.Repository
}

func NewProvider(authRepo *auth.Repository, friendsRepo *friends.Repository, messagesRepo *messages.Repository, callsRepo *calls.Repository) *Provider {
	return &Provider{
		authRepo:     authRepo,
		friendsRepo:  friendsRepo,
		messagesRepo: messagesRepo,
		callsRepo:    callsRepo,
	}
}

func (p *Provider) GetReadyState(ctx context.Context, userID uuid.UUID) (*models.ReadyEvent, error) {
	// Load all data in parallel
	type result struct {
		user          *models.User
		friends       []*models.FriendWithUser
		incoming      []*models.FriendRequestWithUser
		outgoing      []*models.FriendRequestWithUser
		conversations []*models.ConversationWithDetails
		activeCalls   []*models.ActiveCallInfo
		err           error
	}

	ch := make(chan result, 1)

	go func() {
		var r result

		// Get user
		r.user, r.err = p.authRepo.GetUserByID(ctx, userID)
		if r.err != nil {
			ch <- r
			return
		}

		// Get friends
		r.friends, _ = p.friendsRepo.GetFriends(ctx, userID)
		if r.friends == nil {
			r.friends = []*models.FriendWithUser{}
		}

		// Get incoming requests
		r.incoming, _ = p.friendsRepo.GetIncomingRequests(ctx, userID)
		if r.incoming == nil {
			r.incoming = []*models.FriendRequestWithUser{}
		}

		// Get outgoing requests
		r.outgoing, _ = p.friendsRepo.GetOutgoingRequests(ctx, userID)
		if r.outgoing == nil {
			r.outgoing = []*models.FriendRequestWithUser{}
		}

		// Get conversations
		r.conversations, _ = p.messagesRepo.GetUserConversations(ctx, userID)
		if r.conversations == nil {
			r.conversations = []*models.ConversationWithDetails{}
		}

		// Get active calls for user's conversations
		if len(r.conversations) > 0 {
			conversationIDs := make([]uuid.UUID, len(r.conversations))
			for i, c := range r.conversations {
				conversationIDs[i] = c.ID
			}

			activeCalls, _ := p.callsRepo.GetActiveCallsForConversations(ctx, conversationIDs)
			for _, call := range activeCalls {
				participants := make([]uuid.UUID, len(call.Participants))
				for i, p := range call.Participants {
					participants[i] = p.UserID
				}
				r.activeCalls = append(r.activeCalls, &models.ActiveCallInfo{
					CallID:         call.ID,
					ConversationID: call.ConversationID,
					Participants:   participants,
					StartedAt:      call.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
				})
			}
		}
		if r.activeCalls == nil {
			r.activeCalls = []*models.ActiveCallInfo{}
		}

		ch <- r
	}()

	r := <-ch
	if r.err != nil {
		return nil, r.err
	}

	return &models.ReadyEvent{
		User:             r.user,
		Friends:          r.friends,
		IncomingRequests: r.incoming,
		OutgoingRequests: r.outgoing,
		Conversations:    r.conversations,
		ActiveCalls:      r.activeCalls,
	}, nil
}
