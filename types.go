package cryptolike

import (
	"encoding/json"
	"time"
)

// Amounts are decimal strings exactly as the API sends them ("10.50"); nullable ones are *string.
// Parse with a decimal library of your choice — never with float64.

// Currency is a coin of the catalogue.
type Currency struct {
	ID            string  `json:"id"`
	Ticker        string  `json:"ticker"`
	Name          string  `json:"name"`
	Network       string  `json:"network"`
	NetworkName   string  `json:"network_name"`
	Decimals      int     `json:"decimals"`
	Confirmations int     `json:"confirmations"`
	MinDeposit    string  `json:"min_deposit"`
	Maintenance   bool    `json:"maintenance"`
	IconURL       *string `json:"icon_url"`
	// InvoiceEnabled — «available for invoices» in the cabinet (signed requests only; nil in the
	// public catalogue). false: the coin still works for static addresses and payouts, but is not
	// offered on invoices — leave it out of AllowedCoinIDs.
	InvoiceEnabled *bool `json:"invoice_enabled,omitempty"`
}

// Rate is the latest quote of a coin against a fiat currency (1 coin = Rate fiat).
type Rate struct {
	CoinID  string    `json:"coin_id"`
	Ticker  string    `json:"ticker"`
	Network string    `json:"network"`
	Rate    string    `json:"rate"`
	At      time.Time `json:"at"`
	Stale   bool      `json:"stale"`
}

// Balance of one coin.
type Balance struct {
	CoinID    string `json:"coin_id"`
	Ticker    string `json:"ticker"`
	Network   string `json:"network"`
	Available string `json:"available"`
	Pending   string `json:"pending"`
	Hold      string `json:"hold"`
}

// CreateInvoiceRequest — POST /v1/invoices. Amount in Currency (coin ticker or fiat code).
type CreateInvoiceRequest struct {
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	CoinID      string `json:"coin_id,omitempty"`
	Description string `json:"description,omitempty"`
	OrderID     string `json:"order_id,omitempty"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
	ReturnURL   string `json:"return_url,omitempty"`
	WebhookURL  string `json:"webhook_url,omitempty"`
	FeePaidBy   string `json:"fee_paid_by,omitempty"` // merchant | payer
	// Metadata: up to 20 string pairs, returned in the API and webhooks, never shown to the payer.
	Metadata map[string]string `json:"metadata,omitempty"`
	// IdempotencyByOrderID (needs OrderID): the open invoice of the same order is returned (200)
	// instead of a new one; a different amount, or an order that already received funds, is
	// 409 conflict with details.reason order_invoice_mismatch / order_has_payment.
	IdempotencyByOrderID bool `json:"idempotency_by_order_id,omitempty"`
	// AllowedCoinIDs (Currency.ID, 1..100, not with CoinID): the coins the payer may choose from
	// on the payment page; nil — every coin you offer for invoices. The server keeps the ones you
	// offer for invoices that match Currency and drops the rest; none left is 422
	// no_allowed_coins (field allowed_coin_ids). Amount and rate are fixed when the payer picks.
	// An empty non-nil slice is refused before the request (ErrEmptyAllowedCoins).
	AllowedCoinIDs []string `json:"allowed_coin_ids,omitempty"`
}

// Invoice is a payment. Events (the status timeline) is filled by Invoices.Get only.
type Invoice struct {
	ID              string            `json:"id"`
	Status          string            `json:"status"` // new | pending | paid | underpaid | expired | cancelled
	Currency        string            `json:"currency"`
	Amount          string            `json:"amount"`
	CoinID          *string           `json:"coin_id"`
	Ticker          *string           `json:"ticker"`
	Network         *string           `json:"network"`
	Address         *string           `json:"address"`
	AmountCrypto    *string           `json:"amount_crypto"`
	PayerAmount     *string           `json:"payer_amount"`
	PaidAmount      string            `json:"paid_amount"`
	RemainingAmount string            `json:"remaining_amount"`
	OverpaidAmount  string            `json:"overpaid_amount"`
	AmountFiat      *string           `json:"amount_fiat"`
	FiatCurrency    *string           `json:"fiat_currency"`
	RateLocked      *string           `json:"rate_locked"`
	RateLockedAt    *time.Time        `json:"rate_locked_at"`
	LatePayment     bool              `json:"late_payment"`
	FeePaidBy       string            `json:"fee_paid_by"`
	OrderID         *string           `json:"order_id"`
	Description     *string           `json:"description"`
	ReturnURL       *string           `json:"return_url"`
	WebhookURL      *string           `json:"webhook_url"`
	Metadata        map[string]string `json:"metadata"`
	AllowedCoinIDs  []string          `json:"allowed_coin_ids"` // nil — every coin offered for invoices
	CheckoutURL     string            `json:"checkout_url"`
	TTLExpiresAt    time.Time         `json:"ttl_expires_at"`
	ExpiredAt       *time.Time        `json:"expired_at"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Events          []InvoiceEvent    `json:"events,omitempty"`
}

// IsFinal reports whether the status will not change any more (paid, expired, cancelled).
func (i *Invoice) IsFinal() bool {
	return i.Status == "paid" || i.Status == "expired" || i.Status == "cancelled"
}

// InvoiceEvent is one transition of the status timeline.
type InvoiceEvent struct {
	FromStatus *string        `json:"from_status"`
	ToStatus   string         `json:"to_status"`
	At         time.Time      `json:"at"`
	Reason     string         `json:"reason"`
	Details    map[string]any `json:"details"`
}

// CreateAddressRequest — POST /v1/addresses.
type CreateAddressRequest struct {
	CoinID     string `json:"coin_id"`
	CustomerID string `json:"customer_id"`
	Label      string `json:"label,omitempty"`
}

// StaticAddress is a permanent deposit address of one customer.
type StaticAddress struct {
	ID            string     `json:"id"`
	CoinID        string     `json:"coin_id"`
	Ticker        string     `json:"ticker"`
	Network       string     `json:"network"`
	Address       string     `json:"address"`
	CustomerID    string     `json:"customer_id"`
	Label         *string    `json:"label"`
	Status        string     `json:"status"` // active | archived
	TotalReceived string     `json:"total_received"`
	TxCount       int        `json:"tx_count"`
	LastDepositAt *time.Time `json:"last_deposit_at"`
	CreatedAt     time.Time  `json:"created_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`    // last archive; nil when never archived
	ArchiveReason string     `json:"archive_reason,omitempty"` // manual | auto
	RestoredAt    *time.Time `json:"restored_at,omitempty"`    // last restore; nil when never restored
	RestoreReason string     `json:"restore_reason,omitempty"` // manual | deposit
	// RestoreConflictWith is the customer's other active address when an incoming transfer restored
	// this one (both stay active until you archive one).
	RestoreConflictWith string     `json:"restore_conflict_with,omitempty"`
	ArchiveWarnedAt     *time.Time `json:"archive_warned_at,omitempty"` // static_address.archive_warning sent
}

// StaticAddressCard is the address with one page of its deposit history.
type StaticAddressCard struct {
	Address StaticAddress     `json:"address"`
	History Page[Transaction] `json:"history"`
}

// Transaction is one movement on the account.
type Transaction struct {
	ID                    string     `json:"id"`
	Kind                  string     `json:"kind"` // deposit | invoice_payment | withdrawal | adjustment | fee
	CreatedAt             time.Time  `json:"created_at"`
	CoinID                string     `json:"coin_id"`
	Ticker                string     `json:"ticker"`
	Network               string     `json:"network"`
	Amount                string     `json:"amount"`
	Fee                   *string    `json:"fee"`
	NetAmount             *string    `json:"net_amount"`
	Status                string     `json:"status"`
	Confirmations         *int       `json:"confirmations"`
	RequiredConfirmations *int       `json:"required_confirmations"`
	TxID                  *string    `json:"txid"`
	OutputIndex           *int       `json:"output_index"`
	Address               *string    `json:"address"`
	InvoiceID             *string    `json:"invoice_id"`
	OrderID               *string    `json:"order_id"`
	StaticAddressID       *string    `json:"static_address_id"`
	CustomerID            *string    `json:"customer_id"`
	LatePayment           bool       `json:"late_payment"`
	ConfirmedAt           *time.Time `json:"confirmed_at"`
	Comment               *string    `json:"comment"`
}

// EstimateWithdrawalRequest — POST /v1/withdrawals/estimate.
type EstimateWithdrawalRequest struct {
	CoinID    string `json:"coin_id"`
	Amount    string `json:"amount"`
	ToAddress string `json:"to_address,omitempty"`
}

// WithdrawalEstimate is the fee breakdown of a would-be withdrawal.
type WithdrawalEstimate struct {
	CoinID         string  `json:"coin_id"`
	Ticker         string  `json:"ticker"`
	Network        string  `json:"network"`
	Amount         string  `json:"amount"`
	PlatformFee    string  `json:"platform_fee"`
	NetworkFee     string  `json:"network_fee"`
	TotalDebit     string  `json:"total_debit"`
	MinAmount      string  `json:"min_amount"`
	NetworkFeeMode string  `json:"network_fee_mode"` // estimated | actual
	AddressWarning *string `json:"address_warning"`
}

// CreateWithdrawalRequest — POST /v1/withdrawals. PayoutID (your id) makes the request idempotent
// on the platform: the same id with the same parameters returns the existing withdrawal.
type CreateWithdrawalRequest struct {
	CoinID    string `json:"coin_id"`
	Amount    string `json:"amount"`
	ToAddress string `json:"to_address"`
	PayoutID  string `json:"payout_id,omitempty"`
	Comment   string `json:"comment,omitempty"`
}

// Withdrawal is a payout.
type Withdrawal struct {
	ID                  string  `json:"id"`
	Status              string  `json:"status"` // pending | review | awaiting_approval | processing | broadcast | completed | failed | rejected | cancelled
	CoinID              string  `json:"coin_id"`
	Ticker              string  `json:"ticker"`
	Network             string  `json:"network"`
	Amount              string  `json:"amount"`
	PlatformFee         string  `json:"platform_fee"`
	NetworkFeeEstimated string  `json:"network_fee_estimated"`
	NetworkFeeCharged   *string `json:"network_fee_charged"`
	TotalDebit          string  `json:"total_debit"`
	ToAddress           string  `json:"to_address"`
	PayoutID            *string `json:"payout_id"`
	Comment             *string `json:"comment"`
	TxID                *string `json:"txid"`
	Confirmations       int     `json:"confirmations"`
	ReviewReason        *string `json:"review_reason"`
	FailureReason       *string `json:"failure_reason"`
	// FailureCode is the stable code of FailureReason (reverted, compliance, network_rejected,
	// funds_unavailable, dust, address_rejected, signing_policy, network_error, not_confirmed).
	FailureCode    *string    `json:"failure_code"`
	AddressWarning *string    `json:"address_warning"`
	BatchID        *string    `json:"batch_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

// IsFinal reports whether the payout reached an end state.
func (w *Withdrawal) IsFinal() bool {
	switch w.Status {
	case "completed", "failed", "rejected", "cancelled":
		return true
	}
	return false
}

// CreateWebhookEndpointRequest — POST /v1/webhooks/endpoints. Events: types or ["*"].
type CreateWebhookEndpointRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Secret string   `json:"secret,omitempty"` // your own (16–256 chars); omit to get a generated one
}

// UpdateWebhookEndpointRequest — PATCH /v1/webhooks/endpoints/{id}; nil fields are left unchanged.
type UpdateWebhookEndpointRequest struct {
	URL     *string  `json:"url,omitempty"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// WebhookEndpoint is a subscription.
type WebhookEndpoint struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // merchant | invoice
	InvoiceID *string   `json:"invoice_id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WebhookEndpointCreated carries the signing Secret once — store it on your server, never log it.
type WebhookEndpointCreated struct {
	Endpoint WebhookEndpoint `json:"endpoint"`
	Secret   string          `json:"secret"`
}

// WebhookDelivery is one delivery attempt series of an event to an endpoint.
type WebhookDelivery struct {
	ID            string          `json:"id"`
	EndpointID    string          `json:"endpoint_id"`
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
	Attempt       int             `json:"attempt"`
	Status        string          `json:"status"` // pending | delivered | failed | exhausted
	ResponseCode  *int            `json:"response_code"`
	ResponseBody  *string         `json:"response_body"`
	LastError     *string         `json:"last_error"`
	NextAttemptAt *time.Time      `json:"next_attempt_at"`
	LastAttemptAt *time.Time      `json:"last_attempt_at"`
	DeliveredAt   *time.Time      `json:"delivered_at"`
	CreatedAt     time.Time       `json:"created_at"`
}

// EmbedSettings is the checkout widget allowlist (https://cryptolike.net/docs/widget §2).
type EmbedSettings struct {
	AllowedOrigins []string `json:"allowed_origins"`
}
