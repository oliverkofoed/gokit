package sessionkit

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// RefreshAccessCookieName is the cookie name for refresh tokens
const RefreshAccessCookieName = "rt"

// setRefreshTokenCookie is a helper to set the refresh token cookie with consistent settings
func setRefreshTokenCookie(c *web.Context, refreshToken []byte, path string) {
	checkCookieSetup()
	expires := time.Now().Add(30 * 24 * time.Hour) // 30 days

	http.SetCookie(c, &http.Cookie{
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Name:     RefreshAccessCookieName,
		Path:     path,
		Expires:  expires,
		Value:    hex.EncodeToString(refreshToken),
		Domain:   CookieDomain,
	})
}

// CreateRefreshSession creates a new refresh token session, saves it, and sets the cookie
func (s *Sessions) CreateRefreshSession(c *web.Context, path string, userID int64, deviceID []byte, clientInfo string, clientIP []byte, saveSessions func(sessions *Sessions)) {
	// Reuse existing session token logic - Session.Token will be the refresh token
	token := randomBytes(28)
	binary.LittleEndian.PutUint64(token, uint64(userID)) // put userid first

	// build new sessions list, removing old sessions for this device
	nowUnix := time.Now().Unix()
	newSessions := make([]*Session, 0, len(s.sessions.Sessions)+1)
	for _, session := range s.sessions.Sessions {
		if !bytes.Equal(deviceID, session.DeviceID) {
			newSessions = append(newSessions, session)
		}
	}
	newSessions = append(newSessions, &Session{
		Token:      token[8:],
		ClientInfo: clientInfo,
		DeviceID:   deviceID,
		LastIP:     clientIP,
		Created:    nowUnix,
		LastAccess: nowUnix,
	})
	s.sessions.Sessions = newSessions

	// Save the sessions
	saveSessions(s)

	// Set the refresh token cookie
	setRefreshTokenCookie(c, token, path)
}

func getRefreshTokenCookie(c *web.Context) []byte {
	cookie, err := c.Request.Cookie(RefreshAccessCookieName)
	if err != nil {
		return nil
	}

	token, err := hex.DecodeString(cookie.Value)
	if err != nil {
		return nil
	}

	return token
}

func deleteRefreshTokenCookie(c *web.Context, path string) {
	http.SetCookie(c, &http.Cookie{
		Name:     RefreshAccessCookieName,
		Value:    "",
		Path:     path,
		Expires:  time.Unix(0, 0),
		Domain:   CookieDomain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// RevokeRefreshToken validates the refresh token cookie, revokes it, and saves the updated sessions
func RevokeRefreshToken[TUser any](c *web.Context, path string, loadUser func(userId int64) (TUser, *Sessions), saveUser func(user TUser, sessions *Sessions)) {
	// Get refresh token from cookie
	refreshToken := getRefreshTokenCookie(c)
	if refreshToken == nil {
		// Delete cookie anyway
		deleteRefreshTokenCookie(c, path)
		return
	}

	// Extract userID from token (first 8 bytes)
	if len(refreshToken) < 8 {
		deleteRefreshTokenCookie(c, path)
		return
	}
	userID := int64(binary.LittleEndian.Uint64(refreshToken[:8]))

	// Load user and sessions
	user, sessions := loadUser(userID)
	if sessions != nil {
		// Calculate parent hash to identify derived access tokens
		parentHashFull := sha256.Sum256(refreshToken)
		parentHash := parentHashFull[:20]

		// Revoke the refresh token and all derived access tokens
		t := refreshToken[8:]
		newSessions := make([]*Session, 0, len(sessions.sessions.Sessions))
		for _, session := range sessions.sessions.Sessions {
			// Remove if it's the refresh token we're revoking
			if subtle.ConstantTimeCompare(session.Token, t) == 1 {
				continue
			}
			// Remove if it's an access token (40 bytes) derived from this refresh token
			if len(session.Token) == 40 && bytes.Equal(session.Token[20:40], parentHash) {
				continue
			}
			newSessions = append(newSessions, session)
		}
		sessions.sessions.Sessions = newSessions

		// Save the updated sessions
		saveUser(user, sessions)
	}

	// Delete the refresh token cookie
	deleteRefreshTokenCookie(c, path)
}

// RefreshAndCreateAccessToken validates the refresh token cookie,
// rotates it, saves the updated sessions, and returns a new access token
func RefreshAndCreateAccessToken[TUser any](c *web.Context, path string, expiresIn time.Duration, loadUser func(userId int64) (TUser, *Sessions), saveUser func(user TUser, sessions *Sessions)) string {
	// Get refresh token from cookie
	refreshToken := getRefreshTokenCookie(c)
	if refreshToken == nil {
		return ""
	}

	// Extract userID from token (first 8 bytes)
	if len(refreshToken) < 8 {
		return ""
	}
	userID := int64(binary.LittleEndian.Uint64(refreshToken[:8]))

	// Load user and sessions
	user, sessions := loadUser(userID)
	if sessions == nil {
		return ""
	}

	// Rotate the refresh token
	t := refreshToken[8:]
	var newRefreshToken []byte
	var accessToken []byte
	found := false
	for i, session := range sessions.sessions.Sessions {
		if subtle.ConstantTimeCompare(session.Token, t) == 1 {
			// Create new refresh token
			newRefreshToken = randomBytes(28)
			binary.LittleEndian.PutUint64(newRefreshToken, uint64(userID))

			// Update refresh token session
			sessions.sessions.Sessions[i].Token = newRefreshToken[8:]
			sessions.sessions.Sessions[i].LastAccess = time.Now().Unix()
			sessions.sessions.Sessions[i].LastIP = c.ClientIP()

			found = true
			break
		}
	}
	if !found {
		return ""
	}

	// Create parent hash (first 20 bytes of SHA256)
	parentHashFull := sha256.Sum256(newRefreshToken)
	parentHash := parentHashFull[:20]

	// Create new access token: [8B userID][20B random][20B parent hash] = 48 bytes
	accessToken = make([]byte, 48)
	binary.LittleEndian.PutUint64(accessToken, uint64(userID))
	copy(accessToken[8:28], randomBytes(20))
	copy(accessToken[28:48], parentHash)

	// Add new access token session
	// Token stores [20B random][20B parent hash] = 40 bytes
	nowUnix := time.Now().Unix()
	sessions.sessions.Sessions = append(sessions.sessions.Sessions, &Session{
		Token:      accessToken[8:],
		Created:    nowUnix,
		LastAccess: nowUnix,
		LastIP:     c.ClientIP(),
		ClientInfo: c.Request.UserAgent(),
		ExpiresAt:  nowUnix + int64(expiresIn.Seconds()),
	})

	// Save the updated sessions
	saveUser(user, sessions)

	// Set the new refresh token cookie
	setRefreshTokenCookie(c, newRefreshToken, path)

	// Return access token as hex string (48 bytes: userID + random + parent hash)
	return hex.EncodeToString(accessToken)
}

// ValidateAccessToken validates the access token from the Authorization header,
func ValidateAccessToken[TUser any](c *web.Context, loadUser func(userId int64) (TUser, *Sessions), saveUser func(user TUser, sessions *Sessions)) TUser {
	var zero TUser

	// Get access token from Authorization header
	authHeader := c.Request.Header.Get("Authorization")
	if authHeader == "" {
		return zero
	}

	// Extract Bearer token
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return zero
	}
	accessTokenHex := strings.TrimPrefix(authHeader, prefix)

	// Decode access token from hex (48 bytes: 8 userID + 20 random + 20 parent hash)
	accessToken, err := hex.DecodeString(accessTokenHex)
	if err != nil || len(accessToken) != 48 {
		return zero
	}

	// Extract userID from token (first 8 bytes)
	userID := int64(binary.LittleEndian.Uint64(accessToken[:8]))

	// Load user and sessions
	user, sessions := loadUser(userID)
	if sessions == nil {
		return zero
	}

	// Validate access token by looking up in sessions and check expiration
	t := accessToken[8:] // 40 bytes: 20 random + 20 parent hash
	for _, session := range sessions.sessions.Sessions {
		// Check if this is our token
		if subtle.ConstantTimeCompare(session.Token, t) == 1 {
			if session.ExpiresAt == 0 || session.ExpiresAt > time.Now().Unix() {
				return user
			}
		}
	}

	return zero
}
