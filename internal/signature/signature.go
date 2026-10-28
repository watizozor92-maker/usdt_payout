package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func Canonical(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "signature" && values[k] != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}
	return strings.Join(parts, "&")
}

func Sign(values map[string]string, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(Canonical(values)))
	return hex.EncodeToString(m.Sum(nil))
}

func Verify(values map[string]string, secret, got string) bool {
	expected, err := hex.DecodeString(Sign(values, secret))
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(strings.ToLower(got))
	return err == nil && hmac.Equal(expected, actual)
}
