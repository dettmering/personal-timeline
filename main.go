package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	dbPath := getenv("DB_PATH", "/data/timeline.db")
	addr := getenv("LISTEN_ADDR", ":8080")
	apiKey := os.Getenv("API_KEY")

	store, err := OpenStore(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mux := http.NewServeMux()

	api := &API{Store: store, APIKey: apiKey}
	api.Register(mux)

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("listening on %s (db=%s)", addr, dbPath)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
