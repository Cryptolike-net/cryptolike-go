package cryptolike

import (
	"context"
	"net/http"
	"net/url"
)

func pathID(base, id string) string { return base + "/" + url.PathEscape(id) }

// do runs a request and decodes the JSON body into a fresh T; nil on error.
func do[T any](c *Client, ctx context.Context, method, path string, q ListQuery, body any, idempotencyKey string) (*T, error) {
	var out T
	if err := c.Request(ctx, method, path, q, body, idempotencyKey, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Currencies — GET /v1/currencies.
type Currencies struct{ c *Client }

// List returns the coins this key's merchant can use (read).
func (r *Currencies) List(ctx context.Context) ([]Currency, error) {
	var out struct {
		Currencies []Currency `json:"currencies"`
	}
	return out.Currencies, r.c.Request(ctx, http.MethodGet, "/v1/currencies", nil, nil, "", &out)
}

// Rates — GET /v1/rates.
type Rates struct{ c *Client }

// List returns the latest quote of every enabled coin against fiat ("" = the merchant's default fiat).
func (r *Rates) List(ctx context.Context, fiat string) ([]Rate, error) {
	var out struct {
		Rates []Rate `json:"rates"`
	}
	return out.Rates, r.c.Request(ctx, http.MethodGet, "/v1/rates", Q("fiat", fiat), nil, "", &out)
}

// Balances — GET /v1/balances.
type Balances struct{ c *Client }

// List returns the available / pending / on-hold balance per coin.
func (r *Balances) List(ctx context.Context) ([]Balance, error) {
	var out struct {
		Balances []Balance `json:"balances"`
	}
	return out.Balances, r.c.Request(ctx, http.MethodGet, "/v1/balances", nil, nil, "", &out)
}

// Invoices — /v1/invoices.
type Invoices struct{ c *Client }

// Create creates a payment; send the payer to CheckoutURL. idempotencyKey "" generates a UUID.
func (r *Invoices) Create(ctx context.Context, req CreateInvoiceRequest, idempotencyKey string) (*Invoice, error) {
	if req.AllowedCoinIDs != nil && len(req.AllowedCoinIDs) == 0 {
		// A filter that found no coin must not turn into "every coin" (omitempty drops []).
		return nil, ErrEmptyAllowedCoins
	}
	return do[Invoice](r.c, ctx, http.MethodPost, "/v1/invoices", nil, req, idempotencyKey)
}

// Get returns the invoice with its status timeline (Events).
func (r *Invoices) Get(ctx context.Context, id string) (*Invoice, error) {
	return do[Invoice](r.c, ctx, http.MethodGet, pathID("/v1/invoices", id), nil, nil, "")
}

// List returns one page, newest first.
func (r *Invoices) List(ctx context.Context, q ListQuery) (*Page[Invoice], error) {
	return do[Page[Invoice]](r.c, ctx, http.MethodGet, "/v1/invoices", q, nil, "")
}

// Each calls fn for every invoice matching q, page by page; fn returning false stops.
func (r *Invoices) Each(ctx context.Context, q ListQuery, fn func(Invoice) bool) error {
	return iterate(ctx, q, r.List, fn)
}

// Cancel — only status new can be cancelled (otherwise 409 conflict).
func (r *Invoices) Cancel(ctx context.Context, id, idempotencyKey string) (*Invoice, error) {
	return do[Invoice](r.c, ctx, http.MethodPost, pathID("/v1/invoices", id)+"/cancel", nil, nil, idempotencyKey)
}

// CoinOptions returns the coins the payer may choose for an invoice created without a coin — the
// list of the payment page, each with an informative ≈ EstimatedAmount. Show it in your own UI
// and fix the choice with SelectCoin. 409 conflict (details coin_id) once the coin is chosen.
// Permission read.
func (r *Invoices) CoinOptions(ctx context.Context, id string) ([]CoinOption, error) {
	var out struct {
		CoinOptions []CoinOption `json:"coin_options"`
	}
	return out.CoinOptions, r.c.Request(ctx, http.MethodGet, pathID("/v1/invoices", id)+"/coin-options", nil, nil, "", &out)
}

// SelectCoin fixes the coin of an invoice created without a coin — the same choice as the
// payer's on the payment page: allocates the address, prices a fiat amount now and locks the
// rate. It returns the invoice with Address, PayerAmount, PaymentURI, QRPayload and
// RequiredConfirmations. Only once and only while the invoice is new (otherwise 409, also with
// the same coin); a coin outside CoinOptions is 400 (field coin_id); 503 (rate or address not
// available right now) is retried with the same idempotencyKey ("" generates a UUID).
// Permission create_invoice.
func (r *Invoices) SelectCoin(ctx context.Context, id, coinID, idempotencyKey string) (*Invoice, error) {
	body := struct {
		CoinID string `json:"coin_id"`
	}{coinID}
	return do[Invoice](r.c, ctx, http.MethodPost, pathID("/v1/invoices", id)+"/coin", nil, body, idempotencyKey)
}

// ExportCSV returns the whole filtered list as CSV text.
func (r *Invoices) ExportCSV(ctx context.Context, q ListQuery) (string, error) {
	var out string
	return out, r.c.Request(ctx, http.MethodGet, "/v1/invoices", withFormat(q, "csv"), nil, "", &out)
}

// Addresses — /v1/addresses.
type Addresses struct{ c *Client }

// Create allocates one active address per (coin, customer); a second request is 409.
func (r *Addresses) Create(ctx context.Context, req CreateAddressRequest, idempotencyKey string) (*StaticAddress, error) {
	return do[StaticAddress](r.c, ctx, http.MethodPost, "/v1/addresses", nil, req, idempotencyKey)
}

// Get returns the address with one page of its deposit history (q: limit, cursor).
func (r *Addresses) Get(ctx context.Context, id string, q ListQuery) (*StaticAddressCard, error) {
	return do[StaticAddressCard](r.c, ctx, http.MethodGet, pathID("/v1/addresses", id), q, nil, "")
}

// List returns one page, newest first.
func (r *Addresses) List(ctx context.Context, q ListQuery) (*Page[StaticAddress], error) {
	return do[Page[StaticAddress]](r.c, ctx, http.MethodGet, "/v1/addresses", q, nil, "")
}

// Each calls fn for every address matching q.
func (r *Addresses) Each(ctx context.Context, q ListQuery, fn func(StaticAddress) bool) error {
	return iterate(ctx, q, r.List, fn)
}

// Archive stops offering the address. Archived addresses are still checked for incoming
// transfers regularly; a transfer is credited and restores the address automatically (webhook
// static_address.restored). Archiving an archived address is a no-op.
func (r *Addresses) Archive(ctx context.Context, id, idempotencyKey string) (*StaticAddress, error) {
	return do[StaticAddress](r.c, ctx, http.MethodPost, pathID("/v1/addresses", id)+"/archive", nil, nil, idempotencyKey)
}

// Restore makes an archived address active again (restoring an active one is a no-op). When the
// customer already has another active address for the currency it fails with an *APIError 409
// "conflict" whose Details["active_address_id"] names that address — archive it first.
func (r *Addresses) Restore(ctx context.Context, id, idempotencyKey string) (*StaticAddress, error) {
	return do[StaticAddress](r.c, ctx, http.MethodPost, pathID("/v1/addresses", id)+"/restore", nil, nil, idempotencyKey)
}

// ArchiveStaticAddress archives a static address (POST /v1/addresses/{id}/archive; the same call
// as Addresses.Archive): no longer offered, out of real-time scanning, never deleted or re-issued.
// Archived addresses are checked for incoming transfers regularly (every 24 h by default); a
// transfer found is credited as usual and restores the address automatically (webhook
// static_address.restored, reason "deposit"). Archiving an archived address is a no-op. Webhook
// static_address.archived.
func (c *Client) ArchiveStaticAddress(ctx context.Context, id, idempotencyKey string) (*StaticAddress, error) {
	return c.Addresses.Archive(ctx, id, idempotencyKey)
}

// RestoreStaticAddress restores an archived static address (POST /v1/addresses/{id}/restore; the
// same call as Addresses.Restore); restoring an active one is a no-op. *APIError 409 "conflict"
// with Details["active_address_id"] when the customer already has another active address for the
// currency — archive that one first.
func (c *Client) RestoreStaticAddress(ctx context.Context, id, idempotencyKey string) (*StaticAddress, error) {
	return c.Addresses.Restore(ctx, id, idempotencyKey)
}

// ListArchivedStaticAddresses returns one page of archived static addresses (status=archived is
// forced; q: search, coin_id, customer_id, limit, cursor), newest first. q is not modified.
func (c *Client) ListArchivedStaticAddresses(ctx context.Context, q ListQuery) (*Page[StaticAddress], error) {
	return c.Addresses.List(ctx, archivedQuery(q))
}

// EachArchivedStaticAddress calls fn for every archived static address matching q; fn returning
// false stops.
func (c *Client) EachArchivedStaticAddress(ctx context.Context, q ListQuery, fn func(StaticAddress) bool) error {
	return iterate(ctx, archivedQuery(q), c.Addresses.List, fn)
}

func archivedQuery(q ListQuery) ListQuery {
	out := cloneQuery(q)
	out.Set("status", "archived")
	return out
}

// Transactions — /v1/transactions.
type Transactions struct{ c *Client }

// List returns one page, newest first.
func (r *Transactions) List(ctx context.Context, q ListQuery) (*Page[Transaction], error) {
	return do[Page[Transaction]](r.c, ctx, http.MethodGet, "/v1/transactions", q, nil, "")
}

// Each calls fn for every transaction matching q.
func (r *Transactions) Each(ctx context.Context, q ListQuery, fn func(Transaction) bool) error {
	return iterate(ctx, q, r.List, fn)
}

// Get returns one transaction.
func (r *Transactions) Get(ctx context.Context, id string) (*Transaction, error) {
	return do[Transaction](r.c, ctx, http.MethodGet, pathID("/v1/transactions", id), nil, nil, "")
}

// ExportCSV returns the whole filtered list as CSV text (amounts as exact decimal text).
func (r *Transactions) ExportCSV(ctx context.Context, q ListQuery) (string, error) {
	var out string
	return out, r.c.Request(ctx, http.MethodGet, "/v1/transactions", withFormat(q, "csv"), nil, "", &out)
}

// Withdrawals — /v1/withdrawals.
type Withdrawals struct{ c *Client }

// Estimate returns the fees and the total debit for an amount — nothing is created.
func (r *Withdrawals) Estimate(ctx context.Context, req EstimateWithdrawalRequest) (*WithdrawalEstimate, error) {
	return do[WithdrawalEstimate](r.c, ctx, http.MethodPost, "/v1/withdrawals/estimate", nil, req, "")
}

// Create queues a payout (withdraw; verified merchant). Set PayoutID so a repeat with the same
// parameters returns the existing withdrawal instead of creating a second one.
func (r *Withdrawals) Create(ctx context.Context, req CreateWithdrawalRequest, idempotencyKey string) (*Withdrawal, error) {
	return do[Withdrawal](r.c, ctx, http.MethodPost, "/v1/withdrawals", nil, req, idempotencyKey)
}

// Get returns one withdrawal.
func (r *Withdrawals) Get(ctx context.Context, id string) (*Withdrawal, error) {
	return do[Withdrawal](r.c, ctx, http.MethodGet, pathID("/v1/withdrawals", id), nil, nil, "")
}

// List returns one page, newest first.
func (r *Withdrawals) List(ctx context.Context, q ListQuery) (*Page[Withdrawal], error) {
	return do[Page[Withdrawal]](r.c, ctx, http.MethodGet, "/v1/withdrawals", q, nil, "")
}

// Each calls fn for every withdrawal matching q.
func (r *Withdrawals) Each(ctx context.Context, q ListQuery, fn func(Withdrawal) bool) error {
	return iterate(ctx, q, r.List, fn)
}

// Cancel — only before processing starts (pending, review, awaiting_approval).
func (r *Withdrawals) Cancel(ctx context.Context, id, idempotencyKey string) (*Withdrawal, error) {
	return do[Withdrawal](r.c, ctx, http.MethodPost, pathID("/v1/withdrawals", id)+"/cancel", nil, nil, idempotencyKey)
}

// Webhooks groups endpoints and deliveries.
type Webhooks struct {
	Endpoints  *WebhookEndpoints
	Deliveries *WebhookDeliveries
}

// WebhookEndpoints — /v1/webhooks/endpoints.
type WebhookEndpoints struct{ c *Client }

// List returns every endpoint.
func (r *WebhookEndpoints) List(ctx context.Context) ([]WebhookEndpoint, error) {
	var out struct {
		Endpoints []WebhookEndpoint `json:"endpoints"`
	}
	return out.Endpoints, r.c.Request(ctx, http.MethodGet, "/v1/webhooks/endpoints", nil, nil, "", &out)
}

// Create adds an endpoint; the response carries the signing Secret once.
func (r *WebhookEndpoints) Create(ctx context.Context, req CreateWebhookEndpointRequest, idempotencyKey string) (*WebhookEndpointCreated, error) {
	return do[WebhookEndpointCreated](r.c, ctx, http.MethodPost, "/v1/webhooks/endpoints", nil, req, idempotencyKey)
}

// Get returns one endpoint.
func (r *WebhookEndpoints) Get(ctx context.Context, id string) (*WebhookEndpoint, error) {
	return do[WebhookEndpoint](r.c, ctx, http.MethodGet, pathID("/v1/webhooks/endpoints", id), nil, nil, "")
}

// Update changes URL / events / enabled.
func (r *WebhookEndpoints) Update(ctx context.Context, id string, req UpdateWebhookEndpointRequest) (*WebhookEndpoint, error) {
	return do[WebhookEndpoint](r.c, ctx, http.MethodPatch, pathID("/v1/webhooks/endpoints", id), nil, req, "")
}

// Delete removes the endpoint.
func (r *WebhookEndpoints) Delete(ctx context.Context, id string) error {
	return r.c.Request(ctx, http.MethodDelete, pathID("/v1/webhooks/endpoints", id), nil, nil, "", nil)
}

// RotateSecret issues a new secret (shown once); the old one stops verifying immediately.
func (r *WebhookEndpoints) RotateSecret(ctx context.Context, id, idempotencyKey string) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	return out.Secret, r.c.Request(ctx, http.MethodPost, pathID("/v1/webhooks/endpoints", id)+"/rotate-secret", nil, nil, idempotencyKey, &out)
}

// WebhookDeliveries — /v1/webhooks/deliveries.
type WebhookDeliveries struct{ c *Client }

// List returns one page of the delivery journal.
func (r *WebhookDeliveries) List(ctx context.Context, q ListQuery) (*Page[WebhookDelivery], error) {
	return do[Page[WebhookDelivery]](r.c, ctx, http.MethodGet, "/v1/webhooks/deliveries", q, nil, "")
}

// Get returns one delivery.
func (r *WebhookDeliveries) Get(ctx context.Context, id string) (*WebhookDelivery, error) {
	return do[WebhookDelivery](r.c, ctx, http.MethodGet, pathID("/v1/webhooks/deliveries", id), nil, nil, "")
}

// Retry queues the delivery again (also for an already delivered event).
func (r *WebhookDeliveries) Retry(ctx context.Context, id, idempotencyKey string) (*WebhookDelivery, error) {
	return do[WebhookDelivery](r.c, ctx, http.MethodPost, pathID("/v1/webhooks/deliveries", id)+"/retry", nil, nil, idempotencyKey)
}

// Embed — /v1/embed, the checkout widget allowlist (https://cryptolike.net/docs/widget §2).
type Embed struct{ c *Client }

// Get returns the current allowlist.
func (r *Embed) Get(ctx context.Context) (*EmbedSettings, error) {
	return do[EmbedSettings](r.c, ctx, http.MethodGet, "/v1/embed", nil, nil, "")
}

// Set replaces the allowlist as a whole (empty switches embedding off).
func (r *Embed) Set(ctx context.Context, allowedOrigins []string) (*EmbedSettings, error) {
	var out EmbedSettings
	if allowedOrigins == nil {
		allowedOrigins = []string{}
	}
	return &out, r.c.Request(ctx, http.MethodPut, "/v1/embed", nil, EmbedSettings{AllowedOrigins: allowedOrigins}, "", &out)
}
