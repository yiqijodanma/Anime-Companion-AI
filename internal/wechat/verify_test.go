package wechat

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func referenceSignature(token, ts, nonce string) string {
	parts := []string{token, ts, nonce}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func TestCheckSignatureValid(t *testing.T) {
	token, ts, nonce := "mytoken", "1717000000", "rand123"
	require.True(t, CheckSignature(token, ts, nonce, referenceSignature(token, ts, nonce)))
}

func TestCheckSignatureInvalid(t *testing.T) {
	require.False(t, CheckSignature("mytoken", "1717000000", "rand123", "deadbeef"))
}
