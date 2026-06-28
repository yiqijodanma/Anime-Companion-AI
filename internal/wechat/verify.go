package wechat

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"
)

func CheckSignature(token, timestamp, nonce, signature string) bool {
	parts := []string{token, timestamp, nonce}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:]) == signature
}
