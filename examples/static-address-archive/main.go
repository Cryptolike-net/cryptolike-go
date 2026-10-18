// Static-address archive: archive an address you no longer give out, list the archive, restore.
//
//	CRYPTOLIKE_API_KEY_ID=ck_test_… CRYPTOLIKE_API_SECRET=… go run ./examples/static-address-archive <address-id>
//
// An archived address is never deleted or re-issued. It leaves real-time scanning, but the
// platform checks it for incoming transfers regularly (every 24 h by default); a transfer found is
// credited as usual and restores the address automatically (webhook static_address.restored,
// reason "deposit") — see examples/webhook.
package main

import (
	"context"
	"errors"
	"log"
	"net/url"
	"os"
	"time"

	cryptolike "github.com/cryptolike-net/cryptolike-go"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: static-address-archive <address-id>")
	}
	id := os.Args[1]
	api, err := cryptolike.New(cryptolike.Options{}) // credentials from the environment
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	archived, err := api.ArchiveStaticAddress(ctx, id, "archive-"+id) // a repeat is a no-op
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("archived %s at %v (%s)", archived.Address, archived.ArchivedAt, archived.ArchiveReason)

	// The archive: same filters as the address list (search, coin_id, customer_id), newest first.
	err = api.EachArchivedStaticAddress(ctx, url.Values{"customer_id": {archived.CustomerID}}, func(a cryptolike.StaticAddress) bool {
		log.Printf("in the archive: %s %s (%s)", a.ID, a.Address, a.ArchiveReason) // manual | auto
		return true
	})
	if err != nil {
		log.Fatal(err)
	}

	active, err := api.RestoreStaticAddress(ctx, id, "restore-"+id)
	var apiErr *cryptolike.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == 409:
		// The customer got another active address for this coin meanwhile: one active address per
		// (customer, coin). Archive that one first if this is the address to keep.
		log.Printf("restore refused; active address: %v", apiErr.Details["active_address_id"])
	case err != nil:
		log.Fatal(err)
	default:
		log.Printf("active again %s at %v", active.Address, active.RestoredAt)
	}
}
