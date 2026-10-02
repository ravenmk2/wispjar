package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var errInvalidToken = errors.New("invalid token")

type claims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
	Iat int64  `json:"iat"`
}

func SignToken(secret []byte, userID string, ttl time.Duration) (string, error) {
	now := time.Now()
	payload, err := json.Marshal(claims{
		Sub: userID,
		Iat: now.Unix(),
		Exp: now.Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := hmacSign(secret, header+"."+body)
	return header + "." + body + "." + sig, nil
}

func ParseToken(secret []byte, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errInvalidToken
	}
	expected, err := base64.RawURLEncoding.DecodeString(hmacSign(secret, parts[0]+"."+parts[1]))
	if err != nil || !hmac.Equal(sig, expected) {
		return "", errInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errInvalidToken
	}
	var cl claims
	if err := json.Unmarshal(payload, &cl); err != nil {
		return "", errInvalidToken
	}
	if cl.Sub == "" || time.Now().Unix() >= cl.Exp {
		return "", errInvalidToken
	}
	return cl.Sub, nil
}

func hmacSign(secret []byte, data string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
