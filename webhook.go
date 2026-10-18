package cryptolike

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// WebhookEvent is a verified delivery (https://cryptolike.net/docs/webhooks §2). Data is the full state of the object
// at CreatedAt: decode it into the struct of the event type (e.g. struct{ Invoice Invoice }).
type WebhookEvent struct {
	EventID   string          `json:"event_id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// DecodeData decodes Data into v (e.g. *StaticAddressRestoredData for static_address.restored).
func (e *WebhookEvent) DecodeData(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("cryptolike: webhook %s data: %w", e.Type, err)
	}
	return nil
}

// Static-address archive event types (https://cryptolike.net/docs/webhooks §5).
const (
	EventStaticAddressArchived       = "static_address.archived"
	EventStaticAddressRestored       = "static_address.restored"
	EventStaticAddressArchiveWarning = "static_address.archive_warning"
)

// WebhookStaticAddress is the address inside static_address.* and deposit.* webhooks (Coin is the
// ticker).
type WebhookStaticAddress struct {
	ID                  string     `json:"id"`
	Address             string     `json:"address"`
	CoinID              string     `json:"coin_id"`
	Coin                string     `json:"coin"`
	Network             string     `json:"network"`
	CustomerID          string     `json:"customer_id"`
	Label               string     `json:"label,omitempty"`
	Status              string     `json:"status"` // active | archived
	TotalReceived       string     `json:"total_received"`
	TxCount             int        `json:"tx_count"`
	LastDepositAt       *time.Time `json:"last_deposit_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	ArchivedAt          *time.Time `json:"archived_at,omitempty"`
	ArchiveReason       string     `json:"archive_reason,omitempty"` // manual | auto
	RestoredAt          *time.Time `json:"restored_at,omitempty"`
	RestoreReason       string     `json:"restore_reason,omitempty"` // manual | deposit
	RestoreConflictWith string     `json:"restore_conflict_with,omitempty"`
}

// StaticAddressRestoreDeposit is the incoming transfer that reached an archived address (credited
// as usual, with its own deposit.* webhooks).
type StaticAddressRestoreDeposit struct {
	ChainTransactionID string `json:"chain_transaction_id"`
	TxID               string `json:"txid"`
	OutputIndex        int    `json:"output_index"`
	Amount             string `json:"amount"`
	BlockHeight        int64  `json:"block_height"`
}

// StaticAddressArchivedData is the Data of static_address.archived: Reason "manual" (you) or
// "auto" (no incoming transfers for the auto-archive period).
type StaticAddressArchivedData struct {
	Address WebhookStaticAddress `json:"address"`
	Reason  string               `json:"reason"`
}

// StaticAddressRestoredData is the Data of static_address.restored: Reason "manual" or "deposit"
// (with Deposit). ConflictWith — the customer's other active address for the currency: both stay
// active until you archive one.
type StaticAddressRestoredData struct {
	Address      WebhookStaticAddress         `json:"address"`
	Reason       string                       `json:"reason"`
	Deposit      *StaticAddressRestoreDeposit `json:"deposit,omitempty"`
	ConflictWith string                       `json:"conflict_with,omitempty"`
}

// StaticAddressArchiveWarningData is the Data of static_address.archive_warning: archived
// automatically after ArchiveAfter unless an incoming transfer arrives (none since InactiveSince).
type StaticAddressArchiveWarningData struct {
	Address       WebhookStaticAddress `json:"address"`
	Reason        string               `json:"reason"`
	ArchiveAfter  time.Time            `json:"archive_after"`
	InactiveSince time.Time            `json:"inactive_since"`
}

// WebhookReject is the reason a delivery was refused.
type WebhookReject string

const (
	RejectMissingHeader    WebhookReject = "missing_header"
	RejectInvalidTimestamp WebhookReject = "invalid_timestamp"
	RejectTimestampTooOld  WebhookReject = "timestamp_out_of_tolerance"
	RejectInvalidSignature WebhookReject = "invalid_signature"
	RejectInvalidBody      WebhookReject = "invalid_body"
)

const defaultWebhookTolerance = 300 * time.Second

// WebhookVerificationError is returned by VerifyWebhook when the delivery must be rejected.
type WebhookVerificationError struct {
	Reason WebhookReject
	Msg    string
}

func (e *WebhookVerificationError) Error() string { return "cryptolike: webhook rejected: " + e.Msg }

// VerifyOptions tunes VerifyWebhook.
type VerifyOptions struct {
	// Tolerance is the maximum |now − X-Timestamp| (default 5 minutes). Negative disables the
	// check — for offline tests only.
	Tolerance time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
}

// WebhookSignature is the X-Signature the platform computes:
// "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func WebhookSignature(secret, timestamp string, rawBody []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(rawBody)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook checks the signature and timestamp of a delivery and returns the parsed event.
// rawBody must be the bytes exactly as received (io.ReadAll(r.Body)) — never re-serialised JSON.
// On error answer 400/401 and do nothing else; on success deduplicate by EventID.
func VerifyWebhook(headers http.Header, rawBody []byte, secret string, opts *VerifyOptions) (*WebhookEvent, error) {
	if opts == nil {
		opts = &VerifyOptions{}
	}
	if secret == "" {
		return nil, &WebhookVerificationError{RejectInvalidSignature, "webhook secret is empty"}
	}
	ts := headers.Get("X-Timestamp")
	sig := headers.Get("X-Signature")
	if ts == "" {
		return nil, &WebhookVerificationError{RejectMissingHeader, "X-Timestamp header is missing"}
	}
	if sig == "" {
		return nil, &WebhookVerificationError{RejectMissingHeader, "X-Signature header is missing"}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sec < 0 {
		return nil, &WebhookVerificationError{RejectInvalidTimestamp, "X-Timestamp is not Unix seconds"}
	}
	tol := opts.Tolerance
	if tol == 0 {
		tol = defaultWebhookTolerance
	}
	if tol > 0 {
		now := time.Now
		if opts.Now != nil {
			now = opts.Now
		}
		if d := now().Sub(time.Unix(sec, 0)); d > tol || d < -tol {
			return nil, &WebhookVerificationError{RejectTimestampTooOld, fmt.Sprintf("X-Timestamp is outside the %s tolerance", tol)}
		}
	}
	// Exact bytes: "sha256=" prefix and lower-case hex, as the platform sends them.
	if !hmac.Equal([]byte(WebhookSignature(secret, ts, rawBody)), []byte(sig)) {
		return nil, &WebhookVerificationError{RejectInvalidSignature, "X-Signature does not match"}
	}
	var ev WebhookEvent
	if err := json.Unmarshal(rawBody, &ev); err != nil {
		return nil, &WebhookVerificationError{RejectInvalidBody, "body is not a webhook event"}
	}
	if ev.EventID == "" || ev.Type == "" {
		return nil, &WebhookVerificationError{RejectInvalidBody, "body is not a webhook event"}
	}
	return &ev, nil
}
