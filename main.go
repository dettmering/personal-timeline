package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	dbPath := getenv("DB_PATH", "/data/timeline.db")
	addr := getenv("LISTEN_ADDR", ":8080")
	apiKey := os.Getenv("API_KEY")
	webhookURL := os.Getenv("WEBHOOK_URL")
	encKey := os.Getenv("ENCRYPTION_KEY")
	showPermalink := getenvBool("SHOW_PERMALINK", false)
	showQuote := getenvBool("SHOW_QUOTE", false)
	tz := loadTZ()

	store, err := OpenStore(dbPath, tz)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	encOn := false
	if encKey != "" {
		c, err := NewCipher(encKey)
		if err != nil {
			log.Fatalf("encryption key: %v", err)
		}
		store.SetCipher(c)
		encOn = true
	}

	// Order matters: hashes must exist before encrypting, since entry_hash is the AAD.
	n, err := store.BackfillHashes()
	if err != nil {
		log.Fatalf("backfill hashes: %v", err)
	}
	if n > 0 {
		log.Printf("backfilled entry_hash for %d existing rows", n)
	}

	if encOn {
		m, err := store.EncryptBackfill()
		if err != nil {
			log.Fatalf("encrypt backfill: %v", err)
		}
		if m > 0 {
			log.Printf("encrypted %d existing rows", m)
		}
	}

	mux := http.NewServeMux()

	api := &API{
		Store:         store,
		APIKey:        apiKey,
		WebhookURL:    webhookURL,
		TZ:            tz,
		ShowPermalink: showPermalink,
		ShowQuote:     showQuote,
	}
	api.Register(mux)

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	go sealLoop(api)
	go upgradeOTSLoop(api)

	encStr := "off"
	if encOn {
		encStr = "on"
	}
	log.Printf("listening on %s (db=%s, tz=%s, encryption=%s)", addr, dbPath, tz, encStr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func loadTZ() *time.Location {
	name := os.Getenv("TZ")
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("invalid TZ %q: %v — falling back to local", name, err)
		return time.Local
	}
	return loc
}

// sealLoop seals any closed past day that doesn't have a seal yet. It runs once
// at startup and then every 10 minutes. New seals trigger async OTS submission.
func sealLoop(api *API) {
	run := func() {
		sealed, err := api.Store.SealMissing(time.Now())
		if err != nil {
			log.Printf("seal missing: %v", err)
			return
		}
		for _, date := range sealed {
			ds, err := api.Store.GetSeal(date)
			if err != nil {
				log.Printf("get seal %s: %v", date, err)
				continue
			}
			log.Printf("sealed %s (%d entries)", date, ds.EntryCount)
			go api.submitOTS(date, ds.SealHash)
		}
	}
	run()
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		run()
	}
}

// upgradeOTSLoop periodically tries to upgrade pending OTS proofs with the
// Bitcoin attestation once the calendar has it (typically 1-2 hours after seal).
func upgradeOTSLoop(api *API) {
	// Wait a bit after startup before first run to avoid spamming calendars.
	time.Sleep(5 * time.Minute)
	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()
	for {
		UpgradePendingOTS(api.Store)
		<-t.C
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("invalid bool for %s=%q: %v — using default %v", k, v, err, def)
		return def
	}
	return b
}
