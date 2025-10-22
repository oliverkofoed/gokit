package sessionkit

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"go.dedis.ch/protobuf"
	"golang.org/x/crypto/scrypt"
)

type Result int

const ResultValid = Result(1)
const ResultInvalid = Result(2)
const ResultExpired = Result(3)
const ResultMaxTries = Result(4)

func New(sessiondata []byte) *Sessions {
	result := &Sessions{
		sessions: &protobufContainer{},
	}
	err := protobuf.Decode(sessiondata, result.sessions)
	if err != nil {
		panic(err)
	}
	result.RemoveExpiredSessions()
	return result
}

type Sessions struct {
	sessions *protobufContainer
}

type protobufContainer struct {
	// one of these should be set
	Sessions []*Session `protobuf:"1"`

	Password  *passwordContainer  `protobuf:"2"`
	Logincode *logincodeContainer `protobuf:"3"`
}

type passwordContainer struct {
	Password               []byte `protobuf:"1"`
	FailedPasswordAttempts int64  `protobuf:"2"`
}

type logincodeContainer struct {
	Code        string `protobuf:"1"`
	FailedTries int64  `protobuf:"2"`
	Expires     int64  `protobuf:"3"`
}

type Session struct {
	Token      []byte `protobuf:"1"`
	ClientInfo string `protobuf:"2"` // could be: user-agent, sdk version, app version, ...
	DeviceID   []byte `protobuf:"10,opt"`
	LastIP     []byte `protobuf:"11,opt"`
	LastAccess int64  `protobuf:"12,opt"`
	Created    int64  `protobuf:"13,opt"`
	ExpiresAt  int64  `protobuf:"14,opt"` // Server-side expiration for access tokens
	//utc_offset INT8 NOT NULL DEFAULT 0:::INT,
	// push
	IOSPushToken        []byte `protobuf:"51,opt"`
	GooglePlayPushToken []byte `protobuf:"52,opt"`
}

func (s *Session) TokenHex() string {
	return hex.EncodeToString(s.Token)
}

func (s *Session) DeviceIDHex() string {
	return hex.EncodeToString(s.DeviceID)
}

func (s *Session) LastIPNet() net.IP {
	return net.IP(s.LastIP)
}

func (s *Session) LastAccessTime() time.Time {
	return time.Unix(s.LastAccess, 0)
}

func (s *Session) CreatedTime() time.Time {
	return time.Unix(s.Created, 0)
}

func (s *Sessions) RemoveExpiredSessions() {
	now := time.Now().Unix()

	// just a fast loop to check if any are expired
	anyExpired := false
	for _, session := range s.sessions.Sessions {
		if session.ExpiresAt > 0 && session.ExpiresAt < now {
			anyExpired = true
		}
	}
	if !anyExpired {
		return
	}

	// remove expired sessions
	newSessions := make([]*Session, 0, len(s.sessions.Sessions))
	for _, session := range s.sessions.Sessions {
		if session.ExpiresAt > 0 && session.ExpiresAt < now {
			continue
		}
		newSessions = append(newSessions, session)
	}
	s.sessions.Sessions = newSessions
}

func (s *Sessions) Bytes() []byte {
	s.RemoveExpiredSessions()
	buf, err := protobuf.Encode(s.sessions)
	if err != nil {
		panic(err)
	}
	return buf
}

func (s *Sessions) Sessions() []*Session {
	return s.sessions.Sessions
}

func (s *Sessions) HasPassword() bool {
	return s.sessions.Password != nil
}

func (s *Sessions) LoginWithPassword(password string, maxFailedAttempts int64) Result {
	if s.sessions.Password != nil {
		if s.sessions.Password.FailedPasswordAttempts >= maxFailedAttempts {
			s.sessions.Password.FailedPasswordAttempts += 1
			return ResultMaxTries
		} else if ValidPasswordForContainer(s.sessions.Password.Password, password) {
			s.sessions.Password.FailedPasswordAttempts = 0
			return ResultValid
		} else {
			s.sessions.Password.FailedPasswordAttempts += 1
			return ResultInvalid
		}
	}
	return ResultInvalid
}

func (s *Sessions) SetPassword(password string) {
	s.sessions.Password = &passwordContainer{
		Password:               GetPasswordContainer(password),
		FailedPasswordAttempts: 0,
	}
}

func getCurrentLastAccess() int64 {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return day.Unix()
}

func (s *Sessions) GenerateLogincode(letters string, length int, expires time.Duration) string {
	ret := make([]byte, length)
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic(err)
		}
		ret[i] = letters[num.Int64()]
	}

	code := string(ret)

	s.sessions.Logincode = &logincodeContainer{
		Code:        code,
		FailedTries: 0,
		Expires:     time.Now().Add(expires).UTC().Unix(),
	}

	return code
}

func (s *Sessions) ValidateLogincode(code string, maxTries int64) Result {
	if lc := s.sessions.Logincode; lc != nil && lc.Code != "" {
		// has the code expired?
		if lc.Expires < time.Now().UTC().Unix() {
			lc.FailedTries += 1
			return ResultExpired
		}

		// did we try too many times?
		if lc.FailedTries > maxTries {
			lc.FailedTries += 1
			return ResultMaxTries
		}

		// is the code valid
		if subtle.ConstantTimeCompare([]byte(lc.Code), []byte(code)) != 1 {
			lc.FailedTries += 1
			return ResultInvalid
		}
		s.sessions.Logincode = nil
		return ResultValid
	}
	return ResultInvalid
}

// ---------------------

func GetPasswordContainer(password string) []byte {
	version := byte(1)
	N := int32(16384)
	r := int32(8)
	p := int32(1)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(fmt.Errorf("error in generating salt: %s", err))
	}

	key, err := scrypt.Key([]byte(password), salt, int(N), int(r), int(p), 32)
	if err != nil {
		panic(fmt.Errorf("error in deriving password: %s", err))
	}

	buf := new(bytes.Buffer)
	for _, v := range []interface{}{version, N, r, p, salt, key} {
		err := binary.Write(buf, binary.LittleEndian, v)
		if err != nil {
			panic(fmt.Errorf("error creating password: %s", err))
		}
	}

	return buf.Bytes()
}

func ValidPasswordForContainer(container []byte, password string) bool {
	buf := bytes.NewBuffer(container)
	if buf.Len() == 0 {
		return false
	}

	version, err := buf.ReadByte()
	if err != nil {
		return false
	}
	switch version {
	case 1:
		N := int32(16384)
		r := int32(8)
		p := int32(1)
		salt := make([]byte, 16)
		key := make([]byte, 32)

		for _, v := range []interface{}{&N, &r, &p, &salt, &key} {
			err := binary.Read(buf, binary.LittleEndian, v)
			if err != nil {
				return false
			}
		}

		calcKey, err := scrypt.Key([]byte(password), salt, int(N), int(r), int(p), 32)
		if err != nil {
			return false
		}

		if subtle.ConstantTimeCompare(key, calcKey) == 1 {
			return true
		}
	}
	return false
}

var randomIDRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ01234567890")

func randomID(length int) string {
	r := randomBytes(length)
	b := make([]rune, length)
	for i := range b {
		b[i] = randomIDRunes[int(r[i])%len(randomIDRunes)]
	}
	return string(b)
}

func randomBytes(length int) []byte {
	r := make([]byte, length)
	n, err := rand.Read(r)
	if n != length {
		panic(errors.New("did not read enough random data"))
	}
	if err != nil {
		panic(err)
	}
	return r
}
