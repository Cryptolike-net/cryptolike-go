package cryptolike

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// BodySHA256 is the lower-case hex SHA-256 of the raw body bytes.
func BodySHA256(body []byte) string {
	if len(body) == 0 {
		return emptySHA256
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CanonicalString is the string of https://cryptolike.net/docs/auth §3: timestamp, upper-case method, the
// request target exactly as sent and the body hash, joined with LF and no trailing newline.
func CanonicalString(timestamp string, method, pathQuery string, body []byte) string {
	return timestamp + "\n" + strings.ToUpper(method) + "\n" + pathQuery + "\n" + BodySHA256(body)
}

// Sign returns X-Signature: hex HMAC-SHA256 of the canonical string, keyed with the secret as shown
// in the cabinet (not decoded). Checked against the reference vectors
// (https://cryptolike.net/docs/sdk/vectors.json, testdata/vectors.json).
func Sign(secret, timestamp, method, pathQuery string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(CanonicalString(timestamp, method, pathQuery, body)))
	return hex.EncodeToString(mac.Sum(nil))
}
