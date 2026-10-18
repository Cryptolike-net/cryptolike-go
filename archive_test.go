package cryptolike

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestStaticAddressArchiveShortcuts(t *testing.T) {
	const archived = `{"id":"a1","status":"archived","created_at":"2026-09-01T10:00:00Z","archived_at":"2026-10-04T10:00:00Z","archive_reason":"manual"}`
	s, c := newStand(t,
		jsonAnswer(200, archived),
		jsonAnswer(200, `{"id":"a1","status":"active","created_at":"2026-09-01T10:00:00Z","restored_at":"2026-10-04T11:00:00Z","restore_reason":"manual"}`),
		jsonAnswer(200, `{"items":[`+archived+`],"next_cursor":"c2"}`),
		jsonAnswer(200, `{"items":[`+archived+`],"next_cursor":"c2"}`),
		jsonAnswer(200, `{"items":[{"id":"a2","status":"archived","created_at":"2026-09-01T10:00:00Z"}],"next_cursor":null}`),
		jsonAnswer(409, `{"error":{"code":"conflict","message":"customer already has an active address","details":{"field":"customer_id","active_address_id":"a9"}}}`),
	)
	ctx := context.Background()
	a, err := c.ArchiveStaticAddress(ctx, "a/1", "arch-a1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "archived" || a.ArchiveReason != "manual" || a.ArchivedAt == nil {
		t.Errorf("archived = %+v", a)
	}
	r := s.api.seen[0]
	if r.Method != "POST" || r.Target != "/v1/addresses/a%2F1/archive" || r.Body != "" || r.Header.Get("Idempotency-Key") != "arch-a1" {
		t.Errorf("archive request = %s %s %q key=%q", r.Method, r.Target, r.Body, r.Header.Get("Idempotency-Key"))
	}
	if r.Header.Get("X-Signature") != Sign(testSecret, r.Header.Get("X-Timestamp"), "POST", "/v1/addresses/a%2F1/archive", nil) {
		t.Error("archive signature does not cover the request as sent")
	}

	a, err = c.RestoreStaticAddress(ctx, "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "active" || a.RestoreReason != "manual" {
		t.Errorf("restored = %+v", a)
	}
	if r := s.api.seen[1]; r.Target != "/v1/addresses/a1/restore" || r.Header.Get("Idempotency-Key") == "" {
		t.Errorf("restore request = %s key=%q", r.Target, r.Header.Get("Idempotency-Key"))
	}

	// status=archived is forced over the caller's, and the caller's query is left untouched.
	q := url.Values{"search": {"shop"}, "status": {"active"}}
	page, err := c.ListArchivedStaticAddresses(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "a1" || page.NextCursor == nil || *page.NextCursor != "c2" {
		t.Errorf("page = %+v", page)
	}
	if q.Get("status") != "active" {
		t.Error("ListArchivedStaticAddresses modified the caller's query")
	}
	g := s.api.seen[2]
	if g.Method != "GET" || g.Target != "/v1/addresses?search=shop&status=archived" || g.Header.Get("Idempotency-Key") != "" {
		t.Errorf("list request = %s %s", g.Method, g.Target)
	}
	if g.Header.Get("X-Signature") != Sign(testSecret, g.Header.Get("X-Timestamp"), "GET", "/v1/addresses?search=shop&status=archived", nil) {
		t.Error("list signature does not cover the request as sent")
	}

	var ids []string
	if err := c.EachArchivedStaticAddress(ctx, url.Values{"coin_id": {"c1"}}, func(a StaticAddress) bool { ids = append(ids, a.ID); return true }); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a1" || ids[1] != "a2" {
		t.Errorf("ids = %v", ids)
	}
	if s.api.seen[3].Target != "/v1/addresses?coin_id=c1&status=archived" || s.api.seen[4].Target != "/v1/addresses?coin_id=c1&cursor=c2&status=archived" {
		t.Errorf("each targets = %s, %s", s.api.seen[3].Target, s.api.seen[4].Target)
	}
	if len(s.api.seen) != 5 {
		t.Errorf("requests = %d", len(s.api.seen))
	}

	_, err = c.RestoreStaticAddress(ctx, "a1", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Details["active_address_id"] != "a9" {
		t.Errorf("err = %v", err)
	}
}

func signedEvent(t *testing.T, body string) *WebhookEvent {
	t.Helper()
	const secret = "whsec_test_static_archive"
	ts := int64(1758000000)
	h := http.Header{}
	h.Set("X-Timestamp", strconv.FormatInt(ts, 10))
	h.Set("X-Signature", WebhookSignature(secret, strconv.FormatInt(ts, 10), []byte(body)))
	ev, err := VerifyWebhook(h, []byte(body), secret, &VerifyOptions{Now: func() time.Time { return time.Unix(ts, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestStaticAddressWebhookData(t *testing.T) {
	const addr = `{"id":"a1","address":"TAddr","coin_id":"c1","coin":"USDT","network":"tron","customer_id":"u1","status":"active","total_received":"5.5","tx_count":1,"last_deposit_at":"0001-01-01T00:00:00Z","created_at":"2026-04-01T00:00:00Z","restore_reason":"deposit"}`

	ev := signedEvent(t, `{"event_id":"e1","type":"static_address.restored","created_at":"2026-10-04T10:00:00Z","data":{"address":`+addr+`,"reason":"deposit","conflict_with":"a2","deposit":{"chain_transaction_id":"t1","txid":"ab12","output_index":0,"amount":"5.5","block_height":77}}}`)
	if ev.Type != EventStaticAddressRestored {
		t.Fatalf("type = %s", ev.Type)
	}
	var r StaticAddressRestoredData
	if err := ev.DecodeData(&r); err != nil {
		t.Fatal(err)
	}
	if r.Reason != "deposit" || r.ConflictWith != "a2" || r.Address.CustomerID != "u1" || r.Address.Coin != "USDT" || r.Address.RestoreReason != "deposit" ||
		r.Deposit == nil || r.Deposit.TxID != "ab12" || r.Deposit.Amount != "5.5" || r.Deposit.BlockHeight != 77 {
		t.Errorf("restored = %+v", r)
	}

	ev = signedEvent(t, `{"event_id":"e2","type":"static_address.archived","created_at":"2026-10-04T10:00:00Z","data":{"address":`+addr+`,"reason":"auto"}}`)
	var a StaticAddressArchivedData
	if err := ev.DecodeData(&a); err != nil || ev.Type != EventStaticAddressArchived || a.Reason != "auto" || a.Address.ID != "a1" {
		t.Errorf("archived = %+v, %v", a, err)
	}

	ev = signedEvent(t, `{"event_id":"e3","type":"static_address.archive_warning","created_at":"2026-10-04T10:00:00Z","data":{"address":`+addr+`,"reason":"auto","archive_after":"2026-10-11T10:00:00Z","inactive_since":"2026-04-14T10:00:00Z"}}`)
	var w StaticAddressArchiveWarningData
	if err := ev.DecodeData(&w); err != nil || ev.Type != EventStaticAddressArchiveWarning ||
		!w.ArchiveAfter.Equal(time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)) || !w.InactiveSince.Equal(time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("warning = %+v, %v", w, err)
	}

	ev = signedEvent(t, `{"event_id":"e4","type":"static_address.archived","created_at":"2026-10-04T10:00:00Z","data":{"address":"not an object"}}`)
	if err := ev.DecodeData(&a); err == nil {
		t.Error("DecodeData accepted a malformed address")
	}
}
