package sessionkit

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"errors"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

func (s *Sessions) BrowserAccessSession(c *web.Context) (bool, bool) {
	_, token := readSessionCookie(c)
	for _, session := range s.sessions.Sessions {
		if subtle.ConstantTimeCompare(session.Token, token) == 1 {
			now := getCurrentLastAccess()
			updated := false
			if session.LastAccess != now {
				session.LastAccess = now
				updated = true
			}
			if !bytes.Equal(session.LastIP, c.ClientIP()) {
				session.LastIP = c.ClientIP()
				updated = true
			}
			return true, updated
		}
	}
	return false, false
}

func (s *Sessions) BrowserCreateSession(c *web.Context, userID int64, permanentCookie bool) {
	token := randomBytes(20)
	deviceID := BrowserID(c)
	newSessions := make([]*Session, 0, len(s.sessions.Sessions)+1)
	newSessions = append(newSessions, &Session{
		Token:      token,
		ClientInfo: BrowserClientInfo(c),
		DeviceID:   deviceID,
		LastIP:     c.ClientIP(),
		Created:    getCurrentLastAccess(),
	})
	for _, session := range s.sessions.Sessions {
		if !bytes.Equal(deviceID, session.DeviceID) {
			newSessions = append(newSessions, session)
		}
	}
	s.sessions.Sessions = newSessions

	writeSessionCookie(c, userID, token, permanentCookie)
}

func (s *Sessions) BrowserLogout(c *web.Context) {
	_, token := readSessionCookie(c)
	deviceID := BrowserID(c)
	newSessions := make([]*Session, 0, len(s.sessions.Sessions))
	for _, session := range s.sessions.Sessions {
		if !bytes.Equal(deviceID, session.DeviceID) && !bytes.Equal(token, session.Token) {
			newSessions = append(newSessions, session)
		}
	}
	s.sessions.Sessions = newSessions

	deleteCookieValue(c, "sid")
}

func BrowserID(c *web.Context) []byte {
	if bid := getCookieValue(c, "bid"); bid != nil {
		return bid
	} else {
		bid := randomBytes(20)
		setCookieValue(c, "bid", bid, true)
		return bid
	}
}

func BrowserClientInfo(c *web.Context) string {
	return "browser"
}

func BrowserCookieUserID(c *web.Context) int64 {
	userID, _ := readSessionCookie(c)
	return userID
}

func readSessionCookie(c *web.Context) (int64, []byte) {
	if buf := getCookieValue(c, "sid"); buf != nil {
		var id int64
		err := binary.Read(bytes.NewReader(buf), binary.LittleEndian, &id)
		if err == nil {
			return id, buf[8:]
		}
	}
	return 0, []byte{}
}

func writeSessionCookie(c *web.Context, userID int64, token []byte, permanentCookie bool) {
	buf := bytes.NewBuffer(nil)
	binary.Write(buf, binary.LittleEndian, &userID)
	n, err := buf.Write(token)
	if n != len(token) || err != nil {
		panic(errors.New("could not serialize session cookie"))
	}
	setCookieValue(c, "sid", buf.Bytes(), permanentCookie)
}
