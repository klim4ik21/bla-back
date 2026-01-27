package calls

import (
	"time"

	"github.com/livekit/protocol/auth"
)

type LiveKitConfig struct {
	Host      string
	APIKey    string
	APISecret string
}

type LiveKitService struct {
	config LiveKitConfig
}

func NewLiveKitService(config LiveKitConfig) *LiveKitService {
	return &LiveKitService{config: config}
}

func (s *LiveKitService) GenerateToken(roomName, userID, username string) (string, error) {
	at := auth.NewAccessToken(s.config.APIKey, s.config.APISecret)

	at.SetVideoGrant(&auth.VideoGrant{
		RoomJoin: true,
		Room:     roomName,
	}).
		SetIdentity(userID).
		SetName(username).
		SetValidFor(24 * time.Hour)

	return at.ToJWT()
}

func (s *LiveKitService) GetWebSocketURL() string {
	return s.config.Host
}
