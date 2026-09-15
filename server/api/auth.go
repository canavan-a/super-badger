package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RequireAuth enforces Bearer auth once at least one token has been minted
// via `badger token generate` (see cmd/badger) — with none, the API stays
// wide open, matching superbadger's original zero-config local-dev behavior.
// This makes auth opt-in: nothing breaks for an existing install until its
// owner actually runs the CLI.
//
// The token is accepted either as "Authorization: Bearer <token>" (what the
// app's regular HTTP requests send — see app's src/api.ts) or a "?token="
// query param, since browsers can't set custom headers on a WebSocket
// handshake and /stations/:id/ws + /notifications/ws have no other way to
// carry it.
func RequireAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/health" || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		n, err := database.CountAuthTokens(db)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if n == 0 {
			c.Next()
			return
		}

		token := c.Query("token")
		if token == "" {
			if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
				token = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing auth token"})
			return
		}

		rec, err := database.GetAuthTokenByHash(db, hashToken(token))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid auth token"})
			return
		}
		go func(id uint) { _ = database.TouchAuthToken(db, id) }(rec.ID)

		c.Next()
	}
}
