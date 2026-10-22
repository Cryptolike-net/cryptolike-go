// Package cryptolike is the official server-side SDK for the CryptoLike merchant API.
//
//	api, err := cryptolike.New(cryptolike.Options{}) // CRYPTOLIKE_API_KEY_ID + CRYPTOLIKE_API_SECRET from the environment
//	inv, err := api.Invoices.Create(ctx, cryptolike.CreateInvoiceRequest{Amount: "10.50", Currency: "USD", OrderID: "A-1001"}, "")
//
// Every request is signed with the API key (https://cryptolike.net/docs/auth §3); every POST carries an
// Idempotency-Key (yours or a generated UUID, reused across retries); 429 / 5xx / transport errors
// are retried with exponential backoff and jitter, honouring Retry-After. VerifyWebhook checks the
// signature and timestamp of a webhook delivery (https://cryptolike.net/docs/webhooks §3).
//
// The API secret signs requests as you: keep it in your environment or secret manager and use this
// package from your backend only — never from code that ships to a browser or a mobile app.
// Amounts are decimal strings ("10.50"), never floats.
package cryptolike

// Version of the SDK; UserAgent is sent with every request.
const (
	Version   = "0.2.0"
	UserAgent = "cryptolike-sdk-go/" + Version
)
