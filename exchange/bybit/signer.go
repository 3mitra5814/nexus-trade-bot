package bybit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

const defaultRecvWindow = 5000

type Signer struct {
	apiKey     string
	secretKey  string
	recvWindow int64
}

func NewSigner(apiKey, secretKey string) *Signer {
	return &Signer{
		apiKey:     apiKey,
		secretKey:  secretKey,
		recvWindow: defaultRecvWindow,
	}
}

func (s *Signer) APIKey() string {
	return s.apiKey
}

func (s *Signer) RecvWindow() int64 {
	return s.recvWindow
}

func (s *Signer) Timestamp() int64 {
	return time.Now().UnixMilli()
}

func (s *Signer) SignPayload(timestamp int64, payload string) string {
	plainText := strconv.FormatInt(timestamp, 10) + s.apiKey + strconv.FormatInt(s.recvWindow, 10) + payload
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	mac.Write([]byte(plainText))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Signer) WebSocketAuthArgs() []any {
	expires := time.Now().Add(10 * time.Second).UnixMilli()
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	mac.Write([]byte(fmt.Sprintf("GET/realtime%d", expires)))
	return []any{s.apiKey, expires, hex.EncodeToString(mac.Sum(nil))}
}
