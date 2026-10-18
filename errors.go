package cryptolike

import (
	"errors"
	"fmt"
	"time"
)

// ErrEmptyAllowedCoins: CreateInvoiceRequest.AllowedCoinIDs is an empty, non-nil slice. Nothing
// is sent — leave it nil to offer every coin you offer for invoices.
var ErrEmptyAllowedCoins = errors.New("cryptolike: AllowedCoinIDs is empty — nil offers every coin you offer for invoices")

// APIError is a non-2xx response the client could not recover from by retrying. Branch on Code
// (stable machine code, https://cryptolike.net/docs/auth §5 / https://cryptolike.net/docs/api §5); Message is for humans.
type APIError struct {
	Status     int
	Code       string
	Message    string
	Details    map[string]any
	RetryAfter time.Duration // from Retry-After (429, 503), 0 when absent
	RequestID  string        // X-Request-Id — quote it to support
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cryptolike: %d %s: %s", e.Status, e.Code, e.Message)
}

// CodeWithdrawPendingApproval: a payout (create, cancel) with a key whose withdraw permission a
// developer requested and an owner/admin has not approved yet — 403, nothing was created or held,
// the Idempotency-Key is still free. Not retried: retry once the request is approved in the
// cabinet (API keys); the key's other permissions work meanwhile.
const CodeWithdrawPendingApproval = "withdraw_pending_approval"

// WithdrawPendingApproval reports a payout refused because the key's withdraw permission waits
// for an owner/admin's approval (CodeWithdrawPendingApproval).
func (e *APIError) WithdrawPendingApproval() bool { return e.Code == CodeWithdrawPendingApproval }

// Field is details.field of a validation_error, when present.
func (e *APIError) Field() string {
	if e.Details == nil {
		return ""
	}
	f, _ := e.Details["field"].(string)
	return f
}

// TransportError wraps a connection / timeout failure that persisted through all retries.
type TransportError struct {
	Method    string
	PathQuery string
	Err       error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("cryptolike: %s %s: %v", e.Method, e.PathQuery, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}
