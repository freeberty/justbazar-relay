package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/fiatjaf/eventstore/postgresql"
	"github.com/fiatjaf/khatru"
	"github.com/fiatjaf/khatru/policies"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/nbd-wtf/go-nostr"
	"github.com/zebedeeio/go-sdk"
)

var (
	zbd    *zebedee.Client
	config RelayConfig
)

type RelayConfig struct {
	PostgresDatabase string `envconfig:"POSTGRESQL_DATABASE_URL"`
	TicketPriceSats  int64  `envconfig:"TICKET_PRICE_SATS"`
	ZbdApiKey        string `envconfig:"ZBD_API_KEY" required:"true"`
	RelayUrl         string `envconfig:"RELAY_URL"`
	NostrPrivateKey  string `envconfig:"NOSTR_PRIVATE_KEY"`
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	if err := envconfig.Process("", &config); err != nil {
		log.Fatalf("failed to read from env: %v", err)
		return
	}

	// Create relay instance
	relay := khatru.NewRelay()

	// Set basic properties
	relay.Info.Name = "JustBazar Relay"
	relay.Info.Description = "Relay to store paid auction and bid events not nip-15"

	// Initialize database
	db := postgresql.PostgresBackend{DatabaseURL: config.PostgresDatabase}
	if err := db.Init(); err != nil {
		log.Fatalf("failed to initialize database: %v", err)
		return
	}

	// Apply database handlers
	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	relay.QueryEvents = append(relay.QueryEvents, db.QueryEvents)
	relay.CountEvents = append(relay.CountEvents, db.CountEvents)
	relay.DeleteEvent = append(relay.DeleteEvent, db.DeleteEvent)

	// Apply default policies
	policies.ApplySaneDefaults(relay)

	// Custom event validation
	relay.RejectEvent = append(relay.RejectEvent,
		func(ctx context.Context, evt *nostr.Event) (reject bool, msg string) {
			if config.TicketPriceSats > 0 {
				return validateEventPaid(ctx, evt, relay)
			}
			switch evt.Kind {
			case 33222: // Create auction
				return validateAuctionEvent(evt)
			case 1077: // Make bid (should be 1021)
				return validateBidEvent(&db, evt)
			}
			return true, ""
		},
	)

	// User to accept payment for events
	zbd = zebedee.New(config.ZbdApiKey)

	// Set up HTTP handlers
	mux := relay.Router()
	mux.HandleFunc("/pay-for-event", handleEventPayment())
	mux.HandleFunc("/payment-update/{hash}", handlePaymentUpdate(relay))

	// Start server
	fmt.Println("Running auction relay on :3334")
	if err := http.ListenAndServe(":3334", relay); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}