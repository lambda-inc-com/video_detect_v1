package hk_utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

type OPT struct {
	UserID uint64              `json:"userID,omitempty"`
	Token  string              `json:"token"`
	Code   string              `json:"code"`
	Meta   string              `json:"meta"`
	Scope  map[string]struct{} `json:"scope,omitempty"`
}

func GenOPTToken() string {
	//return hack.Base64Encode(noce.Nonce(16), base64.URLEncoding)
	return ""
}

func GenerateSignature(signingString, secret string) string {
	key := []byte(secret)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(signingString))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
