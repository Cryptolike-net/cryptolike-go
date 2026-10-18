package cryptolike

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testSecret = "kZ1Yv7pQ2xL9mN4tR8wE3uH6jB5cF0aD1gS2hK7lP9o"
	testKey    = "ck_test_4f9k2m7p1q8r3s6t0u5v9w2x"
)

type seenReq struct {
	Method, Target, Body string
	Header               http.Header
}

type fakeAPI struct {
	mu      sync.Mutex
	seen    []seenReq
	answers []func(w http.ResponseWriter)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.seen = append(f.seen, seenReq{r.Method, r.URL.RequestURI(), string(body), r.Header.Clone()})
	var a func(http.ResponseWriter)
	if len(f.answers) > 1 {
		a, f.answers = f.answers[0], f.answers[1:]
	} else {
		a = f.answers[0]
	}
	f.mu.Unlock()
	a(w)
}

func jsonAnswer(status int, body string, hdr ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		for i := 0; i+1 < len(hdr); i += 2 {
			w.Header().Set(hdr[i], hdr[i+1])
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

type stand struct {
	api    *fakeAPI
	srv    *httptest.Server
	sleeps []time.Duration
	clock  int64
}

func newStand(t *testing.T, answers ...func(http.ResponseWriter)) (*stand, *Client) {
	t.Helper()
	s := &stand{api: &fakeAPI{answers: answers}, clock: 1758000000}
	s.srv = httptest.NewServer(s.api)
	t.Cleanup(s.srv.Close)
	c, err := New(Options{
		KeyID: testKey, Secret: testSecret, BaseURL: s.srv.URL + "/",
		RetryBase: 100 * time.Millisecond, RetryMax: 400 * time.Millisecond,
		Now:   func() time.Time { s.clock++; return time.Unix(s.clock-1, 0) },
		Sleep: func(_ context.Context, d time.Duration) error { s.sleeps = append(s.sleeps, d); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestCreateInvoiceSignsAndSetsHeaders(t *testing.T) {
	s, c := newStand(t, jsonAnswer(201, `{"id":"inv-1","status":"new","amount":"10.50","checkout_url":"https://pay/x","created_at":"2026-09-16T12:00:00Z"}`))
	ctx := context.Background()
	inv, err := c.Invoices.Create(ctx, CreateInvoiceRequest{Amount: "10.50", Currency: "USD", OrderID: "A-1001"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.ID != "inv-1" || inv.Amount != "10.50" || inv.CreatedAt.IsZero() {
		t.Errorf("invoice = %+v", inv)
	}
	r := s.api.seen[0]
	if r.Method != "POST" || r.Target != "/v1/invoices" {
		t.Errorf("request = %s %s", r.Method, r.Target)
	}
	if r.Body != `{"amount":"10.50","currency":"USD","order_id":"A-1001"}` {
		t.Errorf("body = %s", r.Body)
	}
	h := r.Header
	if h.Get("X-API-Key") != testKey || h.Get("X-Timestamp") != "1758000000" {
		t.Errorf("auth headers = %v", h)
	}
	// https://cryptolike.net/docs/auth §3 vector
	if h.Get("X-Signature") != "7878d965e6eb95b80df12e34e9466d40a8e12aa612653f72760f5dc5c06323a1" {
		t.Errorf("signature = %s", h.Get("X-Signature"))
	}
	if h.Get("Content-Type") != "application/json" || h.Get("X-API-Version") != "v1" || h.Get("Accept") != "application/json" {
		t.Errorf("headers = %v", h)
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "cryptolike-sdk-go/") {
		t.Errorf("user-agent = %s", h.Get("User-Agent"))
	}
	if k := h.Get("Idempotency-Key"); len(k) != 36 {
		t.Errorf("Idempotency-Key = %q", k)
	}

	if _, err := c.Invoices.Create(ctx, CreateInvoiceRequest{Amount: "1", Currency: "USD"}, "order-1"); err != nil {
		t.Fatal(err)
	}
	if s.api.seen[1].Header.Get("Idempotency-Key") != "order-1" {
		t.Error("explicit Idempotency-Key must be sent")
	}
}

func TestGetAndQuerySigning(t *testing.T) {
	s, c := newStand(t, jsonAnswer(200, `{"balances":[{"coin_id":"c","available":"1.25","pending":"0","hold":"0"}],"items":[],"next_cursor":null}`))
	ctx := context.Background()
	bal, err := c.Balances.List(ctx)
	if err != nil || len(bal) != 1 || bal[0].Available != "1.25" {
		t.Fatalf("balances = %+v, %v", bal, err)
	}
	g := s.api.seen[0]
	if g.Header.Get("Idempotency-Key") != "" || g.Header.Get("Content-Type") != "" || g.Body != "" {
		t.Errorf("GET must have no body / idempotency: %+v", g)
	}
	if g.Header.Get("X-Signature") != Sign(testSecret, "1758000000", "GET", "/v1/balances", nil) {
		t.Error("GET signature")
	}
	if _, err := c.Invoices.List(ctx, Q("status", "paid", "from", "2026-09-01T00:00:00Z", "limit", "50")); err != nil {
		t.Fatal(err)
	}
	q := s.api.seen[1]
	want := "/v1/invoices?from=2026-09-01T00%3A00%3A00Z&limit=50&status=paid"
	if q.Target != want {
		t.Errorf("target = %s, want %s", q.Target, want)
	}
	if q.Header.Get("X-Signature") != Sign(testSecret, "1758000001", "GET", want, nil) {
		t.Error("query must be signed as sent")
	}
}

func TestEveryResourceHitsTheDocumentedRoute(t *testing.T) {
	page := `{"items":[],"next_cursor":null,"balances":[],"currencies":[],"rates":[],"endpoints":[],"allowed_origins":[],"secret":"s","history":{"items":[],"next_cursor":null}}`
	s, c := newStand(t, jsonAnswer(200, page))
	ctx := context.Background()
	enabled := false
	calls := []struct {
		fn           func() error
		method, path string
	}{
		{func() error { _, e := c.Currencies.List(ctx); return e }, "GET", "/v1/currencies"},
		{func() error { _, e := c.Rates.List(ctx, "EUR"); return e }, "GET", "/v1/rates?fiat=EUR"},
		{func() error { _, e := c.Balances.List(ctx); return e }, "GET", "/v1/balances"},
		{func() error {
			_, e := c.Addresses.Create(ctx, CreateAddressRequest{CoinID: "c", CustomerID: "u1"}, "")
			return e
		}, "POST", "/v1/addresses"},
		{func() error { _, e := c.Addresses.Get(ctx, "a1", Q("limit", "5")); return e }, "GET", "/v1/addresses/a1?limit=5"},
		{func() error { _, e := c.Addresses.List(ctx, Q("customer_id", "u1")); return e }, "GET", "/v1/addresses?customer_id=u1"},
		{func() error { _, e := c.Addresses.Archive(ctx, "a1", ""); return e }, "POST", "/v1/addresses/a1/archive"},
		{func() error { _, e := c.Addresses.Restore(ctx, "a1", ""); return e }, "POST", "/v1/addresses/a1/restore"},
		{func() error { _, e := c.Transactions.List(ctx, Q("kind", "deposit")); return e }, "GET", "/v1/transactions?kind=deposit"},
		{func() error { _, e := c.Transactions.Get(ctx, "t1"); return e }, "GET", "/v1/transactions/t1"},
		{func() error {
			_, e := c.Withdrawals.Estimate(ctx, EstimateWithdrawalRequest{CoinID: "c", Amount: "1"})
			return e
		}, "POST", "/v1/withdrawals/estimate"},
		{func() error {
			_, e := c.Withdrawals.Create(ctx, CreateWithdrawalRequest{CoinID: "c", Amount: "1", ToAddress: "bc1q", PayoutID: "p-1"}, "")
			return e
		}, "POST", "/v1/withdrawals"},
		{func() error { _, e := c.Withdrawals.Get(ctx, "w1"); return e }, "GET", "/v1/withdrawals/w1"},
		{func() error { _, e := c.Withdrawals.List(ctx, Q("status", "completed")); return e }, "GET", "/v1/withdrawals?status=completed"},
		{func() error { _, e := c.Withdrawals.Cancel(ctx, "w1", ""); return e }, "POST", "/v1/withdrawals/w1/cancel"},
		{func() error { _, e := c.Webhooks.Endpoints.List(ctx); return e }, "GET", "/v1/webhooks/endpoints"},
		{func() error {
			_, e := c.Webhooks.Endpoints.Create(ctx, CreateWebhookEndpointRequest{URL: "https://shop.example/hook", Events: []string{"*"}}, "")
			return e
		}, "POST", "/v1/webhooks/endpoints"},
		{func() error { _, e := c.Webhooks.Endpoints.Get(ctx, "e1"); return e }, "GET", "/v1/webhooks/endpoints/e1"},
		{func() error {
			_, e := c.Webhooks.Endpoints.Update(ctx, "e1", UpdateWebhookEndpointRequest{Enabled: &enabled})
			return e
		}, "PATCH", "/v1/webhooks/endpoints/e1"},
		{func() error { return c.Webhooks.Endpoints.Delete(ctx, "e1") }, "DELETE", "/v1/webhooks/endpoints/e1"},
		{func() error { _, e := c.Webhooks.Endpoints.RotateSecret(ctx, "e1", ""); return e }, "POST", "/v1/webhooks/endpoints/e1/rotate-secret"},
		{func() error { _, e := c.Webhooks.Deliveries.List(ctx, Q("status", "failed")); return e }, "GET", "/v1/webhooks/deliveries?status=failed"},
		{func() error { _, e := c.Webhooks.Deliveries.Get(ctx, "d1"); return e }, "GET", "/v1/webhooks/deliveries/d1"},
		{func() error { _, e := c.Webhooks.Deliveries.Retry(ctx, "d1", ""); return e }, "POST", "/v1/webhooks/deliveries/d1/retry"},
		{func() error { _, e := c.Embed.Get(ctx); return e }, "GET", "/v1/embed"},
		{func() error { _, e := c.Embed.Set(ctx, []string{"https://shop.example"}); return e }, "PUT", "/v1/embed"},
	}
	for i, call := range calls {
		if err := call.fn(); err != nil {
			t.Fatalf("%s: %v", call.path, err)
		}
		r := s.api.seen[i]
		if r.Method != call.method || r.Target != call.path {
			t.Errorf("%s: got %s %s", call.path, r.Method, r.Target)
		}
		if r.Header.Get("X-Signature") != Sign(testSecret, r.Header.Get("X-Timestamp"), call.method, call.path, []byte(r.Body)) {
			t.Errorf("%s: signature does not cover the request as sent", call.path)
		}
		if call.method == "POST" && r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("%s: POST without Idempotency-Key", call.path)
		}
	}
	if s.api.seen[18].Body != `{"enabled":false}` {
		t.Errorf("PATCH body = %s", s.api.seen[18].Body)
	}
}

func TestAddressesRestoreIdempotencyFieldsAndConflict(t *testing.T) {
	s, c := newStand(t,
		jsonAnswer(200, `{"id":"a1","status":"active","created_at":"2026-09-01T10:00:00Z","archived_at":"2026-10-01T10:00:00Z","archive_reason":"auto","restored_at":"2026-10-04T10:00:00Z","restore_reason":"manual"}`),
		jsonAnswer(409, `{"error":{"code":"conflict","message":"customer already has an active address","details":{"field":"customer_id","active_address_id":"a2"}}}`),
	)
	ctx := context.Background()
	a, err := c.Addresses.Restore(ctx, "a1", "restore-a1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "active" || a.ArchiveReason != "auto" || a.RestoreReason != "manual" || a.ArchivedAt == nil || a.RestoredAt == nil ||
		!a.RestoredAt.Equal(time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)) || a.RestoreConflictWith != "" || a.ArchiveWarnedAt != nil {
		t.Errorf("address = %+v", a)
	}
	r := s.api.seen[0]
	if r.Method != "POST" || r.Target != "/v1/addresses/a1/restore" || r.Body != "" || r.Header.Get("Idempotency-Key") != "restore-a1" {
		t.Errorf("request = %s %s %q key=%q", r.Method, r.Target, r.Body, r.Header.Get("Idempotency-Key"))
	}
	if r.Header.Get("X-Signature") != Sign(testSecret, r.Header.Get("X-Timestamp"), "POST", "/v1/addresses/a1/restore", nil) {
		t.Error("restore signature does not cover the request as sent")
	}
	_, err = c.Addresses.Restore(ctx, "a1", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}
	if apiErr.Status != 409 || apiErr.Code != "conflict" || apiErr.Field() != "customer_id" || apiErr.Details["active_address_id"] != "a2" {
		t.Errorf("err = %+v", apiErr)
	}
	if len(s.api.seen) != 2 || len(s.sleeps) != 0 {
		t.Error("409 must not be retried")
	}
	if k := s.api.seen[1].Header.Get("Idempotency-Key"); k == "" || k == "restore-a1" {
		t.Errorf("auto Idempotency-Key = %q", k)
	}
}

func TestAPIErrorNoRetryOn4xx(t *testing.T) {
	s, c := newStand(t, jsonAnswer(400, `{"error":{"code":"validation_error","message":"amount must be positive","details":{"field":"amount"}}}`, "X-Request-Id", "req-1"))
	_, err := c.Invoices.Create(context.Background(), CreateInvoiceRequest{Amount: "-1", Currency: "USD"}, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}
	if apiErr.Status != 400 || apiErr.Code != "validation_error" || apiErr.Field() != "amount" || apiErr.RequestID != "req-1" || apiErr.Message != "amount must be positive" {
		t.Errorf("err = %+v", apiErr)
	}
	if len(s.api.seen) != 1 || len(s.sleeps) != 0 {
		t.Error("4xx must not be retried")
	}
	if strings.Contains(c.String(), testSecret) || strings.Contains(err.Error(), testSecret) {
		t.Error("the secret leaked")
	}
}

// A key whose withdraw permission waits for an owner/admin's approval — a typed 403, not
// retried.
func TestWithdrawPendingApprovalIsTypedAndNotRetried(t *testing.T) {
	s, c := newStand(t, jsonAnswer(403, `{"error":{"code":"withdraw_pending_approval","message":"the payout permission of this API key is waiting for approval by an owner or admin","details":{"required":"withdraw"}}}`))
	_, err := c.Withdrawals.Create(context.Background(), CreateWithdrawalRequest{CoinID: "c1", Amount: "0.1", ToAddress: "bc1q"}, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Code != CodeWithdrawPendingApproval || !apiErr.WithdrawPendingApproval() {
		t.Fatalf("want a withdraw_pending_approval APIError, got %v", err)
	}
	if len(s.api.seen) != 1 || len(s.sleeps) != 0 {
		t.Error("403 withdraw_pending_approval must not be retried")
	}
}

func TestRetries429RetryAfterThen5xxBackoffSameIdempotencyKey(t *testing.T) {
	s, c := newStand(t,
		jsonAnswer(429, `{"error":{"code":"rate_limited","message":"slow"}}`, "Retry-After", "2"),
		jsonAnswer(503, `{"error":{"code":"unavailable","message":"later"}}`),
		jsonAnswer(201, `{"id":"inv-2"}`),
	)
	inv, err := c.Invoices.Create(context.Background(), CreateInvoiceRequest{Amount: "1", Currency: "USD"}, "")
	if err != nil || inv.ID != "inv-2" {
		t.Fatalf("inv = %+v, %v", inv, err)
	}
	if len(s.api.seen) != 3 {
		t.Fatalf("attempts = %d", len(s.api.seen))
	}
	if s.sleeps[0] != 2*time.Second || s.sleeps[1] < 0 || s.sleeps[1] > 200*time.Millisecond {
		t.Errorf("sleeps = %v", s.sleeps)
	}
	k := s.api.seen[0].Header.Get("Idempotency-Key")
	for i, r := range s.api.seen {
		if r.Header.Get("Idempotency-Key") != k {
			t.Error("Idempotency-Key must be reused across retries")
		}
		if want := "175800000" + string(rune('0'+i)); r.Header.Get("X-Timestamp") != want {
			t.Errorf("attempt %d timestamp = %s, want %s", i, r.Header.Get("X-Timestamp"), want)
		}
	}
	if s.api.seen[0].Header.Get("X-Signature") == s.api.seen[1].Header.Get("X-Signature") {
		t.Error("each attempt must be signed afresh")
	}
}

func TestGivesUpAfterMaxRetriesAndSkips501AndLongRetryAfter(t *testing.T) {
	s, c := newStand(t, jsonAnswer(502, `{"error":{"code":"unavailable","message":"bad gateway"}}`))
	var apiErr *APIError
	if _, err := c.Balances.List(context.Background()); !errors.As(err, &apiErr) || apiErr.Status != 502 {
		t.Fatalf("err = %v", err)
	}
	if len(s.api.seen) != 4 { // 1 + MaxRetries(3)
		t.Errorf("attempts = %d", len(s.api.seen))
	}

	s, c = newStand(t, jsonAnswer(501, `{"error":{"code":"not_implemented","message":"no"}}`))
	if _, err := c.Balances.List(context.Background()); !errors.As(err, &apiErr) || len(s.api.seen) != 1 {
		t.Errorf("501 must not be retried: %v / %d", err, len(s.api.seen))
	}

	s, c = newStand(t, jsonAnswer(429, `{"error":{"code":"rate_limited","message":"no"}}`, "Retry-After", "120"))
	if _, err := c.Balances.List(context.Background()); !errors.As(err, &apiErr) || apiErr.RetryAfter != 120*time.Second || len(s.api.seen) != 1 {
		t.Errorf("long Retry-After must stop retrying: %v / %d", err, len(s.api.seen))
	}

	srv := httptest.NewServer(&fakeAPI{answers: []func(http.ResponseWriter){jsonAnswer(429, `{"error":{"code":"rate_limited","message":"no"}}`)}})
	defer srv.Close()
	c, _ = New(Options{KeyID: testKey, Secret: testSecret, BaseURL: srv.URL, MaxRetries: -1, Sleep: func(context.Context, time.Duration) error { t.Error("must not sleep"); return nil }})
	if _, err := c.Balances.List(context.Background()); !errors.As(err, &apiErr) || apiErr.Status != 429 {
		t.Errorf("MaxRetries -1: %v", err)
	}
}

func TestTransportErrorsRetryThenTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // connection refused from now on
	n := 0
	clock := int64(1758000000)
	c, _ := New(Options{KeyID: testKey, Secret: testSecret, BaseURL: srv.URL, MaxRetries: 2,
		Now:   func() time.Time { clock++; return time.Unix(clock-1, 0) },
		Sleep: func(context.Context, time.Duration) error { n++; return nil }})
	_, err := c.Balances.List(context.Background())
	var tErr *TransportError
	if !errors.As(err, &tErr) || n != 2 {
		t.Errorf("err = %v, sleeps = %d", err, n)
	}

	// Cancelled context: no retry, the context error is wrapped.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n = 0
	if _, err := c.Balances.List(ctx); !errors.Is(err, context.Canceled) || n != 0 {
		t.Errorf("cancelled ctx: %v, sleeps = %d", err, n)
	}
}

func TestCSVAndEmptyBodiesAndRedirect(t *testing.T) {
	s, c := newStand(t,
		func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_, _ = io.WriteString(w, "id,amount\n1,2\n")
		},
		func(w http.ResponseWriter) { w.WriteHeader(204) },
		func(w http.ResponseWriter) { w.Header().Set("Location", "https://elsewhere.test/"); w.WriteHeader(302) },
	)
	ctx := context.Background()
	csv, err := c.Transactions.ExportCSV(ctx, Q("kind", "deposit", "limit", "10"))
	if err != nil || csv != "id,amount\n1,2\n" {
		t.Fatalf("csv = %q, %v", csv, err)
	}
	if s.api.seen[0].Target != "/v1/transactions?format=csv&kind=deposit" || s.api.seen[0].Header.Get("Accept") != "text/csv" {
		t.Errorf("export request = %+v", s.api.seen[0])
	}
	if err := c.Webhooks.Endpoints.Delete(ctx, "e1"); err != nil {
		t.Errorf("204: %v", err)
	}
	var apiErr *APIError
	if _, err := c.Balances.List(ctx); !errors.As(err, &apiErr) || apiErr.Status != 302 {
		t.Errorf("redirects must not be followed: %v", err)
	}
}

func TestEachFollowsCursor(t *testing.T) {
	s, c := newStand(t,
		jsonAnswer(200, `{"items":[{"id":"1"},{"id":"2"}],"next_cursor":"c2"}`),
		jsonAnswer(200, `{"items":[{"id":"3"}],"next_cursor":null}`),
	)
	var ids []string
	err := c.Invoices.Each(context.Background(), Q("status", "paid"), func(inv Invoice) bool { ids = append(ids, inv.ID); return true })
	if err != nil || strings.Join(ids, ",") != "1,2,3" {
		t.Fatalf("ids = %v, %v", ids, err)
	}
	if s.api.seen[0].Target != "/v1/invoices?status=paid" || s.api.seen[1].Target != "/v1/invoices?cursor=c2&status=paid" {
		t.Errorf("targets = %s, %s", s.api.seen[0].Target, s.api.seen[1].Target)
	}
}

func TestCredentialsFromEnv(t *testing.T) {
	t.Setenv("CRYPTOLIKE_API_KEY_ID", testKey)
	t.Setenv("CRYPTOLIKE_API_SECRET", testSecret)
	t.Setenv("CRYPTOLIKE_API_URL", "https://api.env.test/")
	c, err := New(Options{})
	if err != nil || c.baseURL != "https://api.env.test" || strings.Contains(c.String(), testSecret) {
		t.Errorf("client = %v, %v", c, err)
	}
	t.Setenv("CRYPTOLIKE_API_SECRET", "")
	if _, err := New(Options{}); err == nil || !strings.Contains(err.Error(), "Secret is required") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("CRYPTOLIKE_API_KEY_ID", "")
	if _, err := New(Options{Secret: testSecret}); err == nil || !strings.Contains(err.Error(), "KeyID is required") {
		t.Errorf("err = %v", err)
	}
}

func TestAuthReplayWaitsForNextSecondAndResigns(t *testing.T) {
	s, c := newStand(t,
		jsonAnswer(401, `{"error":{"code":"auth.replay","message":"this request was already accepted"}}`),
		jsonAnswer(200, `{"balances":[]}`),
	)
	if _, err := c.Balances.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.api.seen) != 2 || len(s.sleeps) != 1 || s.sleeps[0] <= 0 || s.sleeps[0] > 1010*time.Millisecond {
		t.Errorf("attempts = %d, sleeps = %v", len(s.api.seen), s.sleeps)
	}
	if s.api.seen[0].Header.Get("X-Signature") == s.api.seen[1].Header.Get("X-Signature") {
		t.Error("the retry must be signed with a fresh timestamp")
	}
	s, c = newStand(t, jsonAnswer(401, `{"error":{"code":"auth.invalid_signature","message":"no"}}`))
	var apiErr *APIError
	if _, err := c.Balances.List(context.Background()); !errors.As(err, &apiErr) || apiErr.Code != "auth.invalid_signature" || len(s.api.seen) != 1 {
		t.Errorf("other 401s must not be retried: %v / %d", err, len(s.api.seen))
	}
}

func TestRetryInTheSameSecondIsSignedAfresh(t *testing.T) {
	// The server takes the replay slot before the handler runs, so a 5xx has already spent it:
	// repeating the same signature would turn a 500 into 401 auth.replay. Only sleeping moves
	// this clock, so the backoff alone (<= 10 ms) cannot reach the next second.
	api := &fakeAPI{answers: []func(http.ResponseWriter){
		jsonAnswer(503, `{"error":{"code":"unavailable","message":"later"}}`),
		jsonAnswer(200, `{"balances":[]}`),
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	clock := time.Unix(1758000000, 0)
	var sleeps []time.Duration
	c, err := New(Options{
		KeyID: testKey, Secret: testSecret, BaseURL: srv.URL,
		RetryBase: 10 * time.Millisecond, RetryMax: 10 * time.Millisecond,
		Now: func() time.Time { return clock },
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			clock = clock.Add(d)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Balances.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.seen) != 2 {
		t.Fatalf("attempts = %d", len(api.seen))
	}
	first, second := api.seen[0].Header, api.seen[1].Header
	if first.Get("X-Timestamp") != "1758000000" || second.Get("X-Timestamp") != "1758000001" {
		t.Errorf("timestamps = %s, %s", first.Get("X-Timestamp"), second.Get("X-Timestamp"))
	}
	if first.Get("X-Signature") == second.Get("X-Signature") {
		t.Error("the retry repeated the signature: the server would answer 401 auth.replay")
	}
	if want := Sign(testSecret, "1758000001", "GET", "/v1/balances", nil); second.Get("X-Signature") != want {
		t.Errorf("signature = %s, want %s", second.Get("X-Signature"), want)
	}
	if len(sleeps) != 2 || sleeps[1] <= 0 || sleeps[1] > 1010*time.Millisecond {
		t.Errorf("sleeps = %v (backoff, then the wait for the next second)", sleeps)
	}
}

// The payer chooses among AllowedCoinIDs; the field is sent only when set, read back from
// the invoice, and a set with nothing left is a plain 422 APIError (no retry).
func TestCreateInvoiceAllowedCoinIDs(t *testing.T) {
	s, c := newStand(t,
		jsonAnswer(201, `{"id":"inv-2","status":"new","amount":"25","coin_id":null,"allowed_coin_ids":["c-1","c-2"],"checkout_url":"https://pay/y","created_at":"2026-10-01T12:00:00Z"}`),
		jsonAnswer(201, `{"id":"inv-3","status":"new","amount":"25","allowed_coin_ids":null,"checkout_url":"https://pay/z","created_at":"2026-10-01T12:00:00Z"}`),
		jsonAnswer(422, `{"error":{"code":"no_allowed_coins","message":"none of allowed_coin_ids is a coin you offer for invoices in this currency","details":{"field":"allowed_coin_ids"}}}`),
		jsonAnswer(200, `{"currencies":[{"id":"c-1","ticker":"USDT","network":"tron","invoice_enabled":true},{"id":"c-9","ticker":"BTC","network":"bitcoin","invoice_enabled":false},{"id":"c-0","ticker":"LTC","network":"litecoin"}]}`),
	)
	ctx := context.Background()
	inv, err := c.Invoices.Create(ctx, CreateInvoiceRequest{Amount: "25", Currency: "USD", AllowedCoinIDs: []string{"c-1", "c-2"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.api.seen[0].Body; got != `{"amount":"25","currency":"USD","allowed_coin_ids":["c-1","c-2"]}` {
		t.Errorf("body = %s", got)
	}
	if len(inv.AllowedCoinIDs) != 2 || inv.AllowedCoinIDs[1] != "c-2" || inv.CoinID != nil {
		t.Errorf("invoice = %+v", inv)
	}

	inv, err = c.Invoices.Create(ctx, CreateInvoiceRequest{Amount: "25", Currency: "USD"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.api.seen[1].Body, "allowed_coin_ids") {
		t.Errorf("unset field must not be sent: %s", s.api.seen[1].Body)
	}
	if inv.AllowedCoinIDs != nil {
		t.Errorf("null set = every coin, got %v", inv.AllowedCoinIDs)
	}

	_, err = c.Invoices.Create(ctx, CreateInvoiceRequest{Amount: "25", Currency: "BTC", AllowedCoinIDs: []string{"c-1"}}, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 422 || apiErr.Code != "no_allowed_coins" || apiErr.Field() != "allowed_coin_ids" {
		t.Fatalf("err = %v", err)
	}
	if len(s.api.seen) != 3 {
		t.Error("422 must not be retried")
	}

	if _, err := c.Invoices.Create(ctx, CreateInvoiceRequest{Amount: "25", Currency: "USD", AllowedCoinIDs: []string{}}, ""); !errors.Is(err, ErrEmptyAllowedCoins) {
		t.Fatalf("empty set: err = %v", err)
	}
	if len(s.api.seen) != 3 {
		t.Error("an empty set must not reach the API (it would read as every coin)")
	}

	list, err := c.Currencies.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if *list[0].InvoiceEnabled != true || *list[1].InvoiceEnabled != false || list[2].InvoiceEnabled != nil {
		t.Errorf("invoice_enabled = %+v", list)
	}
}
