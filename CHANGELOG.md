# Changelog

All notable changes of this SDK. Versions follow SemVer; the API contract itself (v1) changes only
additively — see https://cryptolike.net/docs/api.

## 0.2.0 — 2026-10-22

- Your own payment page (host-to-host, https://cryptolike.net/docs/h2h):
  - `Invoices.CoinOptions(ctx, id)` → `[]CoinOption` — the coins the payer may choose for an invoice created without a coin, each with an
    informative ≈ amount (`GET /v1/invoices/{id}/coin-options`).
  - `Invoices.SelectCoin(ctx, id, coinID, idempotencyKey)` — fixes the payer's choice (`POST /v1/invoices/{id}/coin`) with an `Idempotency-Key`
    (yours or a generated UUID, the same on every retry) and returns the updated invoice.
  - `Invoice`: `PaymentURI`, `QRPayload`, `Confirmations`, `RequiredConfirmations`; new types `CoinOption`, `CheckoutText`.
  - README: "Your own payment page (host-to-host)".
  - `QRPayload` is the bare deposit address on every network, Bitcoin and Litecoin included (an exchange withdrawal form refuses a
    `bitcoin:…?amount=…` URI); the amount lives in `PaymentURI` — "Open in wallet" and an opt-in "QR with amount" for wallets.
- Request signing and webhook verification are unchanged (`testdata/vectors.json` is the same).

## 0.1.0 — 2026-10-18

- First public release (Go module proxy, pkg.go.dev).
