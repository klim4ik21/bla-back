package calls

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type VoiceConfig struct {
	// WebSocket URL for the voice server
	Host string
	// JWT secret (must match SFU's secret)
	JWTSecret string
}

type VoiceService struct {
	config VoiceConfig
}

// VoiceClaims represents the JWT claims for voice authentication
type VoiceClaims struct {
	RoomID   string `json:"room_id"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func NewVoiceService(config VoiceConfig) *VoiceService {
	return &VoiceService{config: config}
}

func (s *VoiceService) GenerateToken(roomName, userID, username string) (string, error) {
	claims := VoiceClaims{
		RoomID:   roomName,
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.JWTSecret))
}

func (s *VoiceService) GetWebSocketURL() string {
	return s.config.Host
}
