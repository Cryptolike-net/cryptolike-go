// net/http webhook receiver: verify over the RAW body, deduplicate by event_id, acknowledge fast
// (https://cryptolike.net/docs/webhooks §3–§4).
//
//	CRYPTOLIKE_WEBHOOK_SECRET=whsec_… go run ./examples/webhook
package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	cryptolike "github.com/cryptolike-net/cryptolike-go"
)

func main() {
	secret := os.Getenv("CRYPTOLIKE_WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("CRYPTOLIKE_WEBHOOK_SECRET is not set")
	}
	var mu sync.Mutex
	seen := map[string]bool{} // use your database in production

	http.HandleFunc("POST /hooks/cryptolike", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // the bytes as received
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		event, err := cryptolike.VerifyWebhook(r.Header, body, secret, nil) // default tolerance: 5 min
		if err != nil {
			var wErr *cryptolike.WebhookVerificationError
			if errors.As(err, &wErr) && wErr.Reason == cryptolike.RejectInvalidSignature {
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
			return
		}

		mu.Lock()
		dup := seen[event.EventID]
		seen[event.EventID] = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK) // acknowledge first, then act
		if dup {
			return
		}

		// Data is the full state of the object at CreatedAt; deliveries may arrive out of order —
		// decide by the status inside Data, not by arrival.
		switch event.Type {
		case "invoice.paid":
			var data struct {
				Invoice cryptolike.Invoice `json:"invoice"`
			}
			if err := json.Unmarshal(event.Data, &data); err == nil {
				log.Printf("paid order=%v amount=%s", data.Invoice.OrderID, data.Invoice.PaidAmount)
				// fulfil the order
			}
		case "invoice.expired", "invoice.cancelled":
			log.Printf("%s %s", event.Type, event.EventID)
		case cryptolike.EventStaticAddressArchiveWarning:
			var d cryptolike.StaticAddressArchiveWarningData
			if err := event.DecodeData(&d); err == nil {
				log.Printf("will be archived customer=%s after=%s", d.Address.CustomerID, d.ArchiveAfter) // unless a transfer arrives
			}
		case cryptolike.EventStaticAddressArchived:
			var d cryptolike.StaticAddressArchivedData
			if err := event.DecodeData(&d); err == nil {
				log.Printf("archived %s reason=%s", d.Address.ID, d.Reason) // manual | auto — stop showing it to the customer
			}
		case cryptolike.EventStaticAddressRestored:
			var d cryptolike.StaticAddressRestoredData
			if err := event.DecodeData(&d); err == nil {
				// reason "deposit": a transfer reached the archived address; it is credited with its own deposit.* events
				log.Printf("active again %s reason=%s", d.Address.ID, d.Reason)
				if d.ConflictWith != "" {
					log.Printf("the customer has two active addresses; archive one: %s", d.ConflictWith)
				}
			}
		case "ping":
			// test event from the cabinet
		}
	})
	log.Fatal(http.ListenAndServe(":3000", nil))
}
