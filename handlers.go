package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxTextLen = 1000

type API struct {
	Store      *Store
	APIKey     string
	WebhookURL string
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/entries", a.list)
	mux.HandleFunc("POST /api/entries", a.create)
	mux.HandleFunc("PUT /api/entries/{id}", a.update)
	mux.HandleFunc("DELETE /api/entries/{id}", a.delete)
	mux.HandleFunc("GET /api/hashtags", a.hashtags)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
}

func (a *API) hashtags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.Store.ListAllHashtags()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"hashtags": tags})
}

func (a *API) serverTZ() *time.Location {
	tz := os.Getenv("TZ")
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	return loc
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if tag := strings.TrimPrefix(q.Get("hashtag"), "#"); tag != "" {
		entries, err := a.Store.ListByHashtag(tag)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"entries": entries, "hashtag": strings.ToLower(tag)})
		return
	}

	tz := a.serverTZ()
	var day time.Time
	if d := q.Get("date"); d != "" {
		t, err := time.ParseInLocation("2006-01-02", d, tz)
		if err != nil {
			writeErr(w, 400, "invalid date (expected YYYY-MM-DD)")
			return
		}
		day = t
	} else {
		day = time.Now().In(tz)
	}

	entries, err := a.Store.ListByDay(day, tz)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"date":    day.In(tz).Format("2006-01-02"),
		"entries": entries,
	})
}

type createReq struct {
	Text      string `json:"text"`
	Automated bool   `json:"automated"`
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	// API-Key nur für automated-Einträge erzwingen, oder pauschal wenn gesetzt und Header übergeben.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, 400, "cannot read body")
		return
	}
	var req createReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeErr(w, 400, "text is empty")
		return
	}
	if utf8Len(req.Text) > maxTextLen {
		writeErr(w, 400, "text exceeds 1000 characters")
		return
	}
	if req.Automated && !a.checkAPIKey(r) {
		writeErr(w, 401, "invalid or missing API key")
		return
	}

	entry, err := a.Store.Create(req.Text, time.Now(), req.Automated)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.fireWebhook(entry)
	writeJSON(w, 201, entry)
}

func (a *API) fireWebhook(entry *Entry) {
	if a.WebhookURL == "" {
		return
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		log.Printf("webhook marshal: %v", err)
		return
	}
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("POST", a.WebhookURL, bytes.NewReader(payload))
		if err != nil {
			log.Printf("webhook request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("webhook post: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("webhook non-2xx status: %s", resp.Status)
		}
	}()
}

type updateReq struct {
	Text string `json:"text"`
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, 400, "invalid id")
		return
	}
	var req updateReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeErr(w, 400, "text is empty")
		return
	}
	if utf8Len(req.Text) > maxTextLen {
		writeErr(w, 400, "text exceeds 1000 characters")
		return
	}
	entry, err := a.Store.Update(id, req.Text, time.Now(), a.serverTZ())
	if errors.Is(err, ErrNotFound) {
		writeErr(w, 404, "entry not found")
		return
	}
	if errors.Is(err, ErrNotEditable) {
		writeErr(w, 403, "entry is not editable")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, entry)
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, 400, "invalid id")
		return
	}
	err = a.Store.Delete(id, time.Now(), a.serverTZ())
	if errors.Is(err, ErrNotFound) {
		writeErr(w, 404, "entry not found")
		return
	}
	if errors.Is(err, ErrNotDeletable) {
		writeErr(w, 403, "entry is not deletable")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (a *API) checkAPIKey(r *http.Request) bool {
	if a.APIKey == "" {
		return true
	}
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") && strings.TrimPrefix(header, "Bearer ") == a.APIKey {
		return true
	}
	if r.Header.Get("X-API-Key") == a.APIKey {
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
