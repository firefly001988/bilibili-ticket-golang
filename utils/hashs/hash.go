package hash

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HmacSha256 computes HMAC-SHA256 for the given key and data.
func HmacSha256(key string, data string) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// HmacSha256ToHex computes HMAC-SHA256 and returns the hex-encoded string.
func HmacSha256ToHex(key string, data string) string {
	return hex.EncodeToString(HmacSha256(key, data))
}
