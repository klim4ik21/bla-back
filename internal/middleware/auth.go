package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/user/bla-back/internal/auth"
	"github.com/user/bla-back/internal/handlers"
)

func Auth(tokenService *auth.TokenService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				handlers.RespondUnauthorized(w, "Missing authorization header")
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				handlers.RespondUnauthorized(w, "Invalid authorization header format")
				return
			}

			claims, err := tokenService.ValidateAccessToken(parts[1])
			if err != nil {
				handlers.RespondUnauthorized(w, "Invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), "userID", claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

