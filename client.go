package cryptolike

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.cryptolike.net"

// Options configures New. Zero values take the defaults documented on each field.
type Options struct {
	// KeyID (ck_live_… / ck_test_…). Default: env CRYPTOLIKE_API_KEY_ID.
	KeyID string
	// Secret exactly as shown once in the cabinet. Default: env CRYPTOLIKE_API_SECRET. Keep it in
	// your secret manager / environment — never in source code, logs or a browser.
	Secret string
	// BaseURL. Default: env CRYPTOLIKE_API_URL or https://api.cryptolike.net.
	BaseURL string
	// HTTPClient. Default: &http.Client{Timeout: 30s}. Redirects are never followed.
	HTTPClient *http.Client
	// MaxRetries after the first attempt on 429 / 5xx / transport errors (default 3; -1 disables).
	MaxRetries int
	// RetryBase (default 500 ms): delay = min(RetryMax, RetryBase · 2^attempt) with full jitter, or Retry-After.
	RetryBase time.Duration
	// RetryMax caps the backoff (default 8 s). A Retry-After above RetryAfterMax (default 30 s) stops retrying.
	RetryMax      time.Duration
	RetryAfterMax time.Duration
	// APIVersion pins X-API-Version (default "v1").
	APIVersion string
	// AppInfo is appended to the User-Agent, e.g. "my-shop/2.3".
	AppInfo string

	// Now overrides the clock (tests). Sleep overrides the backoff wait (tests).
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
	// NewIdempotencyKey generates the Idempotency-Key of a POST without an explicit key (default: random UUID v4).
	NewIdempotencyKey func() string
}

// Client is a server-side client of the merchant API. One instance per API key; safe for
// concurrent use. Build it with New.
type Client struct {
	keyID, secret, baseURL, apiVersion, userAgent string
	http                                          *http.Client
	maxRetries                                    int
	retryBase, retryMax, retryAfterMax            time.Duration
	now                                           func() time.Time
	sleep                                         func(context.Context, time.Duration) error
	newIdem                                       func() string

	Invoices     *Invoices
	Addresses    *Addresses
	Transactions *Transactions
	Balances     *Balances
	Currencies   *Currencies
	Rates        *Rates
	Withdrawals  *Withdrawals
	Webhooks     *Webhooks
	Embed        *Embed
}

// New builds a Client. Credentials come from opts or the environment.
func New(opts Options) (*Client, error) {
	keyID := firstNonEmpty(opts.KeyID, os.Getenv("CRYPTOLIKE_API_KEY_ID"))
	secret := firstNonEmpty(opts.Secret, os.Getenv("CRYPTOLIKE_API_SECRET"))
	if keyID == "" {
		return nil, errors.New("cryptolike: KeyID is required (Options.KeyID or env CRYPTOLIKE_API_KEY_ID)")
	}
	if secret == "" {
		return nil, errors.New("cryptolike: Secret is required (Options.Secret or env CRYPTOLIKE_API_SECRET)")
	}
	c := &Client{
		keyID:         keyID,
		secret:        secret,
		baseURL:       strings.TrimRight(firstNonEmpty(opts.BaseURL, os.Getenv("CRYPTOLIKE_API_URL"), defaultBaseURL), "/"),
		apiVersion:    firstNonEmpty(opts.APIVersion, "v1"),
		userAgent:     UserAgent,
		http:          opts.HTTPClient,
		maxRetries:    opts.MaxRetries,
		retryBase:     opts.RetryBase,
		retryMax:      opts.RetryMax,
		retryAfterMax: opts.RetryAfterMax,
		now:           opts.Now,
		sleep:         opts.Sleep,
		newIdem:       opts.NewIdempotencyKey,
	}
	if opts.AppInfo != "" {
		c.userAgent += " " + opts.AppInfo
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.maxRetries == 0 {
		c.maxRetries = 3
	} else if c.maxRetries < 0 {
		c.maxRetries = 0
	}
	if c.retryBase == 0 {
		c.retryBase = 500 * time.Millisecond
	}
	if c.retryMax == 0 {
		c.retryMax = 8 * time.Second
	}
	if c.retryAfterMax == 0 {
		c.retryAfterMax = 30 * time.Second
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	if c.newIdem == nil {
		c.newIdem = newUUID
	}
	c.Invoices = &Invoices{c}
	c.Addresses = &Addresses{c}
	c.Transactions = &Transactions{c}
	c.Balances = &Balances{c}
	c.Currencies = &Currencies{c}
	c.Rates = &Rates{c}
	c.Withdrawals = &Withdrawals{c}
	c.Webhooks = &Webhooks{Endpoints: &WebhookEndpoints{c}, Deliveries: &WebhookDeliveries{c}}
	c.Embed = &Embed{c}
	return c, nil
}

// String never reveals the secret.
func (c *Client) String() string {
	return fmt.Sprintf("cryptolike.Client{KeyID:%q BaseURL:%q Secret:[redacted]}", c.keyID, c.baseURL)
}

// Request is a signed call with retries; out (a pointer) receives the JSON body, or the raw text
// when it is a *string. Prefer the typed resources; use Request for an endpoint added after this
// SDK version. idempotencyKey applies to POST; "" generates one.
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, body any, idempotencyKey string, out any) error {
	method = strings.ToUpper(method)
	pathQuery := path
	if enc := query.Encode(); enc != "" {
		pathQuery += "?" + enc
	}
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return fmt.Errorf("cryptolike: encode body: %w", err)
		}
	}
	if method == http.MethodPost && idempotencyKey == "" {
		idempotencyKey = c.newIdem()
	}
	_, wantText := out.(*string)

	var lastTs int64
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return &TransportError{method, pathQuery, err}
		}
		now := c.now()
		if now.Unix() == lastTs {
			// Timestamps are whole seconds, so a retry inside the same second would repeat the
			// signature byte for byte and the server accepts a signature once: a retry after a
			// genuine 5xx would come back as 401 auth.replay. Wait the second out first.
			if err := c.sleep(ctx, untilNextSecond(now)); err != nil {
				return &TransportError{method, pathQuery, err}
			}
			now = c.now()
		}
		lastTs = now.Unix()
		ts := strconv.FormatInt(now.Unix(), 10)
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+pathQuery, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("cryptolike: build request: %w", err)
		}
		if wantText {
			req.Header.Set("Accept", "text/csv")
		} else {
			req.Header.Set("Accept", "application/json")
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("X-API-Version", c.apiVersion)
		req.Header.Set("X-API-Key", c.keyID)
		req.Header.Set("X-Timestamp", ts)
		req.Header.Set("X-Signature", Sign(c.secret, ts, method, pathQuery, data))
		if len(data) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		res, err := c.doNoRedirect(req)
		if err != nil {
			if ctx.Err() != nil || attempt >= c.maxRetries {
				return &TransportError{method, pathQuery, err}
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return &TransportError{method, pathQuery, err}
			}
			continue
		}
		respBody, readErr := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if readErr != nil {
			if attempt >= c.maxRetries {
				return &TransportError{method, pathQuery, readErr}
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return &TransportError{method, pathQuery, err}
			}
			continue
		}

		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return decodeOut(res, respBody, out)
		}
		apiErr := &APIError{Status: res.StatusCode, Code: "http_error", Message: "HTTP " + strconv.Itoa(res.StatusCode), RequestID: res.Header.Get("X-Request-Id")}
		if strings.Contains(res.Header.Get("Content-Type"), "application/json") {
			var eb errorBody
			if json.Unmarshal(respBody, &eb) == nil && eb.Error.Code != "" {
				apiErr.Code, apiErr.Message, apiErr.Details = eb.Error.Code, eb.Error.Message, eb.Error.Details
			}
		}
		if ra, ok := parseRetryAfter(res.Header.Get("Retry-After")); ok {
			apiErr.RetryAfter = ra
		}
		if apiErr.Status == http.StatusUnauthorized && apiErr.Code == "auth.replay" && attempt < c.maxRetries {
			// Timestamps are whole seconds: an identical request within the same second carries the
			// same signature and the server accepts a signature once. Wait for the next second and
			// sign afresh (POSTs keep their Idempotency-Key, so nothing is created twice).
			if err := c.sleep(ctx, untilNextSecond(c.now())); err != nil {
				return apiErr
			}
			continue
		}
		if !retryable(res.StatusCode) || attempt >= c.maxRetries {
			return apiErr
		}
		delay := c.backoff(attempt)
		if apiErr.RetryAfter > 0 {
			delay = apiErr.RetryAfter
		}
		if delay > c.retryAfterMax {
			return apiErr
		}
		if err := c.sleep(ctx, delay); err != nil {
			return apiErr
		}
	}
}

func (c *Client) doNoRedirect(req *http.Request) (*http.Response, error) {
	// Never follow a redirect: the signature covers the original target only.
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return hc.Do(req)
}

func (c *Client) backoff(attempt int) time.Duration {
	capMs := math.Min(float64(c.retryMax/time.Millisecond), float64(c.retryBase/time.Millisecond)*math.Pow(2, float64(attempt)))
	if capMs <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(capMs)+1))
	if err != nil {
		return time.Duration(capMs) * time.Millisecond
	}
	return time.Duration(n.Int64()) * time.Millisecond
}

func decodeOut(res *http.Response, body []byte, out any) error {
	if out == nil || len(body) == 0 {
		return nil
	}
	if s, ok := out.(*string); ok {
		*s = string(body)
		return nil
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		return fmt.Errorf("cryptolike: unexpected Content-Type %q", res.Header.Get("Content-Type"))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("cryptolike: decode response: %w", err)
	}
	return nil
}

func untilNextSecond(now time.Time) time.Duration {
	return now.Truncate(time.Second).Add(time.Second + 10*time.Millisecond).Sub(now)
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status != http.StatusNotImplemented)
}

func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	sec, err := strconv.ParseFloat(v, 64)
	if err != nil || sec < 0 {
		return 0, false
	}
	return time.Duration(sec * float64(time.Second)), true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newUUID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		panic("cryptolike: crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Page is one page of a cursor-paginated list; pass NextCursor back as "cursor" for the next one.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// ListQuery is the filter set of a list endpoint, as query parameters named in the API reference (https://cryptolike.net/docs/api)
// (status, coin_id, from, to, limit, cursor, …). Build it with url.Values or Q.
type ListQuery = url.Values

// Q builds a ListQuery from key/value pairs: cryptolike.Q("status", "paid", "limit", "50").
func Q(kv ...string) ListQuery {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	return q
}

// Iterate calls fn for every item of a paginated list, page by page; fn returning false stops.
func iterate[T any](ctx context.Context, q ListQuery, fetch func(ctx context.Context, q ListQuery) (*Page[T], error), fn func(T) bool) error {
	q = cloneQuery(q)
	for {
		page, err := fetch(ctx, q)
		if err != nil {
			return err
		}
		for _, it := range page.Items {
			if !fn(it) {
				return nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

func cloneQuery(q ListQuery) ListQuery {
	out := url.Values{}
	for k, vs := range q {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func withFormat(q ListQuery, format string) ListQuery {
	q = cloneQuery(q)
	q.Set("format", format)
	q.Del("cursor")
	q.Del("limit")
	return q
}
