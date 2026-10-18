# cryptolike (Go SDK)

Official **server-side** SDK for the CryptoLike merchant API. Go ≥ 1.23, standard library only.

```bash
go get github.com/cryptolike-net/cryptolike-go
```

```go
import cryptolike "github.com/cryptolike-net/cryptolike-go"
```

- Signed requests (`X-API-Key` / `X-Timestamp` / `X-Signature`, HMAC-SHA256 — [Authentication](https://cryptolike.net/docs/auth))
- `Idempotency-Key` generated for every `POST` (pass your own to make retries exact)
- Retries with exponential backoff and jitter on `429` (honours `Retry-After`), `5xx` and transport errors; `context` aware
- Typed resources mirroring the [API reference](https://cryptolike.net/docs/api); amounts are decimal strings, never floats
- `VerifyWebhook` — signature and timestamp check of webhook deliveries ([Webhooks](https://cryptolike.net/docs/webhooks))

## The secret stays on your server

> The API secret signs requests *as you*: whoever has it can create payments, read balances and
> change your webhooks. Use this package from your backend only; never embed the secret in a
> mobile app or anything served to a browser. Your page needs only the invoice id /
> `CheckoutURL` your server obtained — see [Payment widget §1](https://cryptolike.net/docs/widget).

`New` reads `CRYPTOLIKE_API_KEY_ID` and `CRYPTOLIKE_API_SECRET` from the environment when
`Options` leaves them empty; the secret is never logged and `Client.String()` redacts it, which
covers `%v`, `%+v`, `%s`, `%q` and `log.Print`. `%#v` prints the struct literal — do not dump a
client that way.

## Quickstart — to the first confirmed payment

```go
api, err := cryptolike.New(cryptolike.Options{}) // credentials from the environment

// 1. Create a payment on your server; send the customer to CheckoutURL.
inv, err := api.Invoices.Create(ctx, cryptolike.CreateInvoiceRequest{
    Amount: "10.50", Currency: "USD", OrderID: "A-1001", ReturnURL: "https://shop.example/orders/A-1001",
}, "order-A-1001") // Idempotency-Key — a retry never creates a second payment
http.Redirect(w, r, inv.CheckoutURL, http.StatusFound)

// 2. Learn the outcome from the webhook (below) — or poll:
detail, err := api.Invoices.Get(ctx, inv.ID)
if detail.Status == "paid" { fulfil(*detail.OrderID) }
```

```go
// 3. Webhook receiver — verify over the RAW body, dedupe by EventID, answer 2xx fast.
http.HandleFunc("POST /hooks/cryptolike", func(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
    event, err := cryptolike.VerifyWebhook(r.Header, body, os.Getenv("CRYPTOLIKE_WEBHOOK_SECRET"), nil)
    if err != nil { w.WriteHeader(http.StatusUnauthorized); return }
    if !alreadySeen(event.EventID) { handle(event) } // event.Type == "invoice.paid" → fulfil
    w.WriteHeader(http.StatusOK)
})
```

Full examples: [`examples/create-payment`](examples/create-payment/main.go), [`examples/webhook`](examples/webhook/main.go).

## Resources

| Resource | Methods |
|---|---|
| `api.Invoices` | `Create(ctx, req, idempotencyKey)`, `Get`, `List(ctx, q)`, `Each(ctx, q, fn)`, `Cancel`, `ExportCSV` |
| `api.Addresses` | `Create`, `Get(ctx, id, q)`, `List`, `Each`, `Archive(ctx, id, idempotencyKey)`, `Restore(ctx, id, idempotencyKey)` |
| `api` (archive) | `ArchiveStaticAddress`, `RestoreStaticAddress`, `ListArchivedStaticAddresses(ctx, q)`, `EachArchivedStaticAddress(ctx, q, fn)` |
| `api.Transactions` | `List`, `Each`, `Get`, `ExportCSV` |
| `api.Balances` / `api.Currencies` / `api.Rates` | `List(ctx)` (`Rates.List(ctx, "EUR")`) |
| `api.Withdrawals` | `Estimate`, `Create`, `Get`, `List`, `Each`, `Cancel` |
| `api.Webhooks.Endpoints` | `List`, `Create`, `Get`, `Update`, `Delete`, `RotateSecret` |
| `api.Webhooks.Deliveries` | `List`, `Get`, `Retry` |
| `api.Embed` | `Get`, `Set(ctx, origins)` — checkout widget allowlist |
| `api.Request(ctx, method, path, query, body, idempotencyKey, &out)` | any endpoint, signed |

### Static-address archive

Archiving stops offering an address and takes it out of real-time scanning; it is never deleted, and its address is never issued again. The platform checks archived addresses for incoming transfers regularly (every 24 h by default): a transfer found is credited as usual and restores the address automatically (webhook `static_address.restored`, reason `deposit`). Addresses with no incoming transfers for the platform's period (180 days by default) are archived automatically after a warning 7 days earlier (`static_address.archive_warning`, then `static_address.archived` with reason `auto`).

```go
_, err := api.ArchiveStaticAddress(ctx, id, "")                        // POST /v1/addresses/{id}/archive — a repeat is a no-op
page, err := api.ListArchivedStaticAddresses(ctx, url.Values{"search": {"shop"}}) // status=archived; coin_id, customer_id, limit, cursor
err = api.EachArchivedStaticAddress(ctx, nil, func(a cryptolike.StaticAddress) bool { return true })
_, err = api.RestoreStaticAddress(ctx, id, "")                         // *APIError 409 conflict, Details["active_address_id"]: archive that one first
```

Decode a verified archive event with `event.DecodeData(&d)` into `StaticAddressArchivedData`, `StaticAddressRestoredData` (`Reason`, `Deposit`, `ConflictWith`) or `StaticAddressArchiveWarningData` (`ArchiveAfter`, `InactiveSince`); types `EventStaticAddressArchived|Restored|ArchiveWarning`. Full example: `examples/static-address-archive`, `examples/webhook`.

List filters are `url.Values` named as in the API — `cryptolike.Q("status", "paid", "limit", "50")`.
`List` returns a `Page[T]` (`Items`, `NextCursor`); `Each` walks every page. Nullable fields are
pointers; timestamps are `time.Time`.

## Payer chooses the coin

An invoice in a fiat currency (or a ticker on several networks) without `CoinID` lets the payer
choose the coin on the payment page; each option shows an estimate (≈) — the exact amount and
rate are fixed when the payer picks. `AllowedCoinIDs` narrows the choice for one invoice; leave
it nil to offer every coin you offer for invoices:

```go
coins, _ := api.Currencies.List(ctx) // signed: each coin carries InvoiceEnabled
var ids []string
for _, c := range coins {
    if (c.InvoiceEnabled == nil || *c.InvoiceEnabled) && !c.Maintenance && c.Ticker == "USDT" {
        ids = append(ids, c.ID)
    }
}
inv, err := api.Invoices.Create(ctx, cryptolike.CreateInvoiceRequest{
    Amount: "49.90", Currency: "USD", AllowedCoinIDs: ids,
}, "")
// inv.AllowedCoinIDs — the kept set; nil = every coin offered for invoices.
```

The server keeps the ids you offer for invoices («Available for invoices» in the cabinet → Coins)
that match `Currency`, drops the rest; none left → `*APIError` 422 `no_allowed_coins`
(`Field() == "allowed_coin_ids"`). Not combinable with `CoinID` (400). An empty non-nil slice is
refused before the request (`ErrEmptyAllowedCoins`) — a filter that found nothing never turns
into "every coin".

## Errors

A non-2xx response is an `*APIError` (`Status`, `Code`, `Message`, `Details`, `Field()`,
`RetryAfter`, `RequestID`); a transport failure that survived the retries is a `*TransportError`
(unwraps to the cause, including `context.Canceled`). Use `errors.As`. Codes:
[Authentication §5](https://cryptolike.net/docs/auth), [API §5](https://cryptolike.net/docs/api).
A payout with a key whose `withdraw` a developer requested and an owner/admin has not approved yet is
`403` `CodeWithdrawPendingApproval` (`apiErr.WithdrawPendingApproval()`): nothing was created, not
retried — approve the request in the cabinet (API keys); the key's other permissions work meanwhile.

## Retries and idempotency

`429` (after `Retry-After`, or backoff), `5xx` except `501`, and connection errors are retried up to
`MaxRetries` (default 3; `-1` disables) with `min(RetryMax, RetryBase · 2^attempt)` full-jitter
backoff, stopping when the context ends. Every `POST` carries an `Idempotency-Key` (yours, or a
UUID generated once per call and reused on each retry), so a retry never creates a second invoice,
address or withdrawal. Each attempt is signed with a fresh timestamp. Redirects are never followed.
A `401 auth.replay` — two identical requests signed within the same second share a signature —
waits for the next second and signs afresh.

## Options

```go
cryptolike.Options{
    KeyID, Secret,                     // default: CRYPTOLIKE_API_KEY_ID / CRYPTOLIKE_API_SECRET
    BaseURL,                           // default: CRYPTOLIKE_API_URL or https://api.cryptolike.net
    HTTPClient: &http.Client{Timeout: 30 * time.Second},
    MaxRetries: 3, RetryBase: 500 * time.Millisecond, RetryMax: 8 * time.Second, RetryAfterMax: 30 * time.Second,
    APIVersion: "v1",                  // X-API-Version pin
    AppInfo: "my-shop/2.3",            // appended to User-Agent: cryptolike-sdk-go/<version>
}
```

## Webhook verification

`VerifyWebhook(headers, rawBody, secret, &VerifyOptions{Tolerance: 5 * time.Minute})` returns the
event (`EventID`, `Type`, `CreatedAt`, `Data` as `json.RawMessage`) or a `*WebhookVerificationError`
with `Reason` (`missing_header` | `invalid_timestamp` | `timestamp_out_of_tolerance` |
`invalid_signature` | `invalid_body`). Answer `401`/`400` on failure, `2xx` on success, and
deduplicate by `EventID` — deliveries are at least once and may arrive out of order.
