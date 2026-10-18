package cryptolike

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// testdata/vectors.json — the reference vectors generated from the platform's own signer
// (published at https://cryptolike.net/docs/sdk/vectors.json).
type vectorsFile struct {
	Webhook struct {
		Vectors []struct {
			Name      string            `json:"name"`
			Secret    string            `json:"secret"`
			Timestamp int64             `json:"timestamp"`
			Body      string            `json:"body"`
			Headers   map[string]string `json:"headers"`
			Signature string            `json:"signature"`
			Valid     bool              `json:"valid"`
		} `json:"vectors"`
	} `json:"webhook"`
	APIRequest struct {
		Vectors []struct {
			Name            string `json:"name"`
			Secret          string `json:"secret"`
			Timestamp       int64  `json:"timestamp"`
			Method          string `json:"method"`
			PathQuery       string `json:"path_query"`
			Body            string `json:"body"`
			BodySHA256      string `json:"body_sha256"`
			CanonicalString string `json:"canonical_string"`
			Signature       string `json:"signature"`
		} `json:"vectors"`
	} `json:"api_request"`
}

func loadVectors(t *testing.T) vectorsFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectorsFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return v
}

func headersOf(m map[string]string) http.Header {
	h := http.Header{}
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

func TestAPIRequestVectors(t *testing.T) {
	vs := loadVectors(t).APIRequest.Vectors
	if len(vs) < 6 {
		t.Fatalf("expected ≥ 6 api_request vectors, got %d", len(vs))
	}
	for _, v := range vs {
		ts := strconv.FormatInt(v.Timestamp, 10)
		body := []byte(v.Body)
		if got := BodySHA256(body); got != v.BodySHA256 {
			t.Errorf("%s: body sha256 = %s, want %s", v.Name, got, v.BodySHA256)
		}
		if got := CanonicalString(ts, v.Method, v.PathQuery, body); got != v.CanonicalString {
			t.Errorf("%s: canonical = %q, want %q", v.Name, got, v.CanonicalString)
		}
		if got := Sign(v.Secret, ts, v.Method, v.PathQuery, body); got != v.Signature {
			t.Errorf("%s: signature = %s, want %s", v.Name, got, v.Signature)
		}
		if got := Sign(v.Secret, ts, "post", v.PathQuery, body); v.Method == "POST" && got != v.Signature {
			t.Errorf("%s: lower-case method must sign the same", v.Name)
		}
	}
}

func TestWebhookVectors(t *testing.T) {
	vs := loadVectors(t).Webhook.Vectors
	valid, invalid := 0, 0
	for _, v := range vs {
		ts := strconv.FormatInt(v.Timestamp, 10)
		body := []byte(v.Body)
		opts := &VerifyOptions{Now: func() time.Time { return time.Unix(v.Timestamp, 0) }}
		ev, err := VerifyWebhook(headersOf(v.Headers), body, v.Secret, opts)
		if v.Valid {
			valid++
			if got := WebhookSignature(v.Secret, ts, body); got != v.Signature {
				t.Errorf("%s: signature = %s, want %s", v.Name, got, v.Signature)
			}
			if err != nil {
				t.Errorf("%s: unexpected reject: %v", v.Name, err)
				continue
			}
			if ev.EventID != v.Headers["X-Event-Id"] || ev.Type != v.Headers["X-Event-Type"] {
				t.Errorf("%s: event = %+v", v.Name, ev)
			}
			if _, err := VerifyWebhook(headersOf(v.Headers), body, v.Secret, &VerifyOptions{Tolerance: -1}); err != nil {
				t.Errorf("%s: tolerance disabled: %v", v.Name, err)
			}
			continue
		}
		invalid++
		var wErr *WebhookVerificationError
		if !errors.As(err, &wErr) || wErr.Reason != RejectInvalidSignature {
			t.Errorf("%s: want invalid_signature, got %v", v.Name, err)
		}
	}
	if valid < 5 || invalid < 4 {
		t.Fatalf("expected ≥ 5 valid and ≥ 4 invalid vectors, got %d / %d", valid, invalid)
	}
}

func reasonOf(t *testing.T, err error) WebhookReject {
	t.Helper()
	var wErr *WebhookVerificationError
	if !errors.As(err, &wErr) {
		t.Fatalf("want WebhookVerificationError, got %v", err)
	}
	return wErr.Reason
}

func TestWebhookToleranceAndMalformed(t *testing.T) {
	v := loadVectors(t).Webhook.Vectors[0]
	body := []byte(v.Body)
	h := headersOf(v.Headers)
	at := func(off int64) *VerifyOptions {
		return &VerifyOptions{Now: func() time.Time { return time.Unix(v.Timestamp+off, 0) }}
	}
	if _, err := VerifyWebhook(h, body, v.Secret, at(301)); reasonOf(t, err) != RejectTimestampTooOld {
		t.Error("301 s late must be rejected")
	}
	if _, err := VerifyWebhook(h, body, v.Secret, at(-301)); reasonOf(t, err) != RejectTimestampTooOld {
		t.Error("301 s early must be rejected")
	}
	if _, err := VerifyWebhook(h, body, v.Secret, at(300)); err != nil {
		t.Errorf("300 s must pass: %v", err)
	}
	if _, err := VerifyWebhook(h, body, v.Secret, &VerifyOptions{Tolerance: 60 * time.Second, Now: at(61).Now}); reasonOf(t, err) != RejectTimestampTooOld {
		t.Error("custom tolerance")
	}
	if _, err := VerifyWebhook(h, body, v.Secret, nil); reasonOf(t, err) != RejectTimestampTooOld {
		t.Error("the 2023 vector must be stale against the wall clock")
	}
	without := func(name string) http.Header {
		c := h.Clone()
		c.Del(name)
		return c
	}
	off := &VerifyOptions{Tolerance: -1}
	if _, err := VerifyWebhook(without("X-Signature"), body, v.Secret, off); reasonOf(t, err) != RejectMissingHeader {
		t.Error("missing signature")
	}
	if _, err := VerifyWebhook(without("X-Timestamp"), body, v.Secret, off); reasonOf(t, err) != RejectMissingHeader {
		t.Error("missing timestamp")
	}
	with := func(name, val string) http.Header {
		c := h.Clone()
		c.Set(name, val)
		return c
	}
	if _, err := VerifyWebhook(with("X-Timestamp", "1700000000.5"), body, v.Secret, off); reasonOf(t, err) != RejectInvalidTimestamp {
		t.Error("fractional timestamp")
	}
	if _, err := VerifyWebhook(with("X-Signature", "sha256="), body, v.Secret, off); reasonOf(t, err) != RejectInvalidSignature {
		t.Error("empty signature")
	}
	if _, err := VerifyWebhook(h, body, "", off); reasonOf(t, err) != RejectInvalidSignature {
		t.Error("empty secret")
	}
	var pretty map[string]any
	_ = json.Unmarshal(body, &pretty)
	re, _ := json.MarshalIndent(pretty, "", "  ")
	if _, err := VerifyWebhook(h, re, v.Secret, off); reasonOf(t, err) != RejectInvalidSignature {
		t.Error("re-serialised body must not verify")
	}
	junk := []byte("[1,2,3]")
	hj := http.Header{}
	hj.Set("X-Timestamp", "1700000000")
	hj.Set("X-Signature", WebhookSignature(v.Secret, "1700000000", junk))
	if _, err := VerifyWebhook(hj, junk, v.Secret, off); reasonOf(t, err) != RejectInvalidBody {
		t.Error("non-event body")
	}
}
