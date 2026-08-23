package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func signBody(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
