// Create a payment and print the page to send the payer to.
//
//	CRYPTOLIKE_API_KEY_ID=ck_test_… CRYPTOLIKE_API_SECRET=… go run ./examples/create-payment
//
// Runs on your server. The browser only ever receives CheckoutURL / the invoice id.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	cryptolike "github.com/cryptolike-net/cryptolike-go"
)

func main() {
	api, err := cryptolike.New(cryptolike.Options{}) // credentials from the environment
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	inv, err := api.Invoices.Create(ctx, cryptolike.CreateInvoiceRequest{
		Amount: "10.50", Currency: "USD", OrderID: "A-1001", ReturnURL: "https://shop.example/orders/A-1001",
	}, "order-A-1001") // Idempotency-Key: your order id — a retry never creates a second payment
	if err != nil {
		var apiErr *cryptolike.APIError
		if errors.As(err, &apiErr) {
			// Stable machine code + human message; Field() for validation errors.
			log.Fatalf("API %d %s (%s): %s", apiErr.Status, apiErr.Code, apiErr.Field(), apiErr.Message)
		}
		log.Fatal(err)
	}
	fmt.Println("send the customer to", inv.CheckoutURL)

	// The payer chooses the coin on the payment page, among the ones you allow for this invoice
	// (here: every stablecoin you offer for invoices). Omit AllowedCoinIDs to offer them all.
	coins, err := api.Currencies.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var stable []string
	for _, c := range coins {
		offered := c.InvoiceEnabled == nil || *c.InvoiceEnabled
		if offered && !c.Maintenance && (c.Ticker == "USDT" || c.Ticker == "USDC") {
			stable = append(stable, c.ID)
		}
	}
	if len(stable) > 0 {
		choice, err := api.Invoices.Create(ctx, cryptolike.CreateInvoiceRequest{
			Amount: "10.50", Currency: "USD", OrderID: "A-1002", AllowedCoinIDs: stable,
		}, "order-A-1002")
		if err != nil {
			log.Fatal(err) // 422 no_allowed_coins: none of the ids is offered for invoices in USD
		}
		fmt.Println("payer picks one of", len(choice.AllowedCoinIDs), "coins at", choice.CheckoutURL)
	}

	detail, err := api.Invoices.Get(ctx, inv.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("status", detail.Status, "events", len(detail.Events))
}
