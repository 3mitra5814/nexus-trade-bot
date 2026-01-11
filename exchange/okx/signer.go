package okx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

type Signer struct {
	apiKey     string
	secretKey  string
	passphrase string
}

func NewSigner(apiKey, secretKey, passphrase string) *Signer {
	return &Signer{apiKey: apiKey, secretKey: secretKey, passphrase: passphrase}
}

func (s *Signer) APIKey() string {
	return s.apiKey
}

func (s *Signer) Passphrase() string {
	return s.passphrase
}

func (s *Signer) RestTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func (s *Signer) Sign(timestamp, method, requestPath, body string) string {
	payload := timestamp + method + requestPath + body
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	mac.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Signer) WebSocketLoginArgs() []map[string]string {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sign := s.Sign(timestamp, http.MethodGet, "/users/self/verify", "")
	return []map[string]string{{
		"apiKey":     s.apiKey,
		"passphrase": s.passphrase,
		"timestamp":  timestamp,
		"sign":       sign,
	}}
}
