package sessionkit

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"net"
)

func (s *Sessions) TokenAccessSession(token []byte, clientIP net.IP) (bool, bool) {
	t := token[8:]
	for _, session := range s.sessions.Sessions {
		if subtle.ConstantTimeCompare(session.Token, t) == 1 {
			now := getCurrentLastAccess()
			updated := false
			if session.LastAccess != now {
				session.LastAccess = now
				updated = true
			}
			if !bytes.Equal(session.LastIP, clientIP) {
				session.LastIP = clientIP
				updated = true
			}
			return true, updated
		}
	}
	return false, false
}

func (s *Sessions) TokenReplaceUserID(token []byte, newUserID int64) []byte {
	binary.LittleEndian.PutUint64(token, uint64(newUserID)) // first 8 bytes is userid
	return token
}

func (s *Sessions) TokenCreateSession(userID int64, deviceID []byte, clientInfo string, clientIP net.IP) []byte {
	token := randomBytes(28)

	binary.LittleEndian.PutUint64(token, uint64(userID)) // first 8 bytes is userid

	newSessions := make([]*Session, 0, len(s.sessions.Sessions)+1)
	newSessions = append(newSessions, &Session{
		Token:      token[8:],
		ClientInfo: clientInfo,
		DeviceID:   deviceID,
		LastIP:     clientIP,
		Created:    getCurrentLastAccess(),
	})
	for _, session := range s.sessions.Sessions {
		if !bytes.Equal(deviceID, session.DeviceID) {
			newSessions = append(newSessions, session)
		}
	}
	s.sessions.Sessions = newSessions

	return token
}

func (s *Sessions) TokenSetIOSPushToken(token []byte, iosPushToken []byte) {
	t := token[8:]
	for _, session := range s.sessions.Sessions {
		if subtle.ConstantTimeCompare(session.Token, t) == 1 {
			now := getCurrentLastAccess()
			session.LastAccess = now
			session.IOSPushToken = iosPushToken
		}
	}
}

func (s *Sessions) TokenSetGooglePlayPushToken(token []byte, googlePlayPushToken []byte) {
	t := token[8:]
	for _, session := range s.sessions.Sessions {
		if subtle.ConstantTimeCompare(session.Token, t) == 1 {
			now := getCurrentLastAccess()
			session.LastAccess = now
			session.GooglePlayPushToken = googlePlayPushToken
		}
	}
}

func (s *Sessions) TokenGetIOSPushToken(token []byte) []byte {
	t := token[8:]
	for _, session := range s.sessions.Sessions {
		if subtle.ConstantTimeCompare(session.Token, t) == 1 {
			return session.IOSPushToken
		}
	}
	return nil
}

func TokenUserID(token []byte) int64 {
	if len(token) < 8 {
		return -1
	} else {
		return int64(binary.LittleEndian.Uint64(token))
	}
}

func (s *Sessions) TokenLogout(token []byte) {
	t := token[8:]
	newSessions := make([]*Session, 0, len(s.sessions.Sessions))
	for _, session := range s.sessions.Sessions {
		if !bytes.Equal(session.Token, t) {
			newSessions = append(newSessions, session)
		}
	}
	s.sessions.Sessions = newSessions
}
