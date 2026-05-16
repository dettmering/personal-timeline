package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxTextLen = 10000

type API struct {
	Store      *Store
	APIKey     string
	WebhookURL string
	TZ         *time.Location
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/entries", a.list)
	mux.HandleFunc("POST /api/entries", a.create)
	mux.HandleFunc("GET /api/entries/{id}", a.getEntry)
	mux.HandleFunc("PUT /api/entries/{id}", a.update)
	mux.HandleFunc("DELETE /api/entries/{id}", a.delete)
	mux.HandleFunc("GET /api/hashtags", a.hashtags)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/verify", a.verify)
	mux.HandleFunc("GET /api/seals", a.listSeals)
	mux.HandleFunc("GET /api/seals/{date}", a.getSeal)
	mux.HandleFunc("GET /api/seals/{date}/proof.ots", a.getSealProof)
	mux.HandleFunc("POST /api/seals/{date}", a.triggerSeal)
	mux.HandleFunc("GET /dashboard", a.dashboardHTML)
}

func (a *API) dashboardHTML(w http.ResponseWriter, r *http.Request) {
	b, err := staticFiles.ReadFile("static/dashboard.html")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
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
	if a.TZ != nil {
		return a.TZ
	}
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
		limit := 0
		if l := q.Get("limit"); l != "" {
			n, err := strconv.Atoi(l)
			if err != nil || n < 1 {
				writeErr(w, 400, "invalid 'limit' (expected positive integer)")
				return
			}
			limit = n
		}
		entries, err := a.Store.ListByHashtag(tag, limit)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"entries": entries, "hashtag": strings.ToLower(tag)})
		return
	}

	tz := a.serverTZ()

	if _, ok := q["q"]; ok {
		limit := 20
		if l := q.Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		entries, err := a.Store.SearchEntries(strings.TrimSpace(q.Get("q")), limit)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"entries": entries})
		return
	}

	if fromStr, toStr := q.Get("from"), q.Get("to"); fromStr != "" || toStr != "" {
		if fromStr == "" || toStr == "" {
			writeErr(w, 400, "both 'from' and 'to' are required for range queries")
			return
		}
		from, err := time.ParseInLocation("2006-01-02", fromStr, tz)
		if err != nil {
			writeErr(w, 400, "invalid 'from' date (expected YYYY-MM-DD)")
			return
		}
		to, err := time.ParseInLocation("2006-01-02", toStr, tz)
		if err != nil {
			writeErr(w, 400, "invalid 'to' date (expected YYYY-MM-DD)")
			return
		}
		if to.Before(from) {
			writeErr(w, 400, "'to' must not be before 'from'")
			return
		}
		entries, err := a.Store.ListByRange(from, to, tz)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"from":    fromStr,
			"to":      toStr,
			"entries": entries,
		})
		return
	}

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

func (a *API) getEntry(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, 400, "invalid id")
		return
	}
	entry, err := a.Store.Get(id)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, 404, "entry not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, entry)
}

// flexFloat unmarshals JSON numbers or quoted strings into float64.
type flexFloat struct{ V float64 }

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	if len(b) > 1 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return err
		}
		f.V = v
		return nil
	}
	return json.Unmarshal(b, &f.V)
}

type createReq struct {
	Text      string     `json:"text"`
	Automated bool       `json:"automated"`
	Lat       *flexFloat `json:"lat,omitempty"`
	Lon       *flexFloat `json:"lon,omitempty"`
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
		writeErr(w, 400, "text exceeds 10000 characters")
		return
	}
	if req.Automated && !a.checkAPIKey(r) {
		writeErr(w, 401, "invalid or missing API key")
		return
	}
	if (req.Lat == nil) != (req.Lon == nil) {
		writeErr(w, 400, "lat and lon must be provided together")
		return
	}
	if req.Lat != nil {
		if req.Lat.V < -90 || req.Lat.V > 90 {
			writeErr(w, 400, "lat out of range (-90..90)")
			return
		}
		if req.Lon.V < -180 || req.Lon.V > 180 {
			writeErr(w, 400, "lon out of range (-180..180)")
			return
		}
	}

	var lat, lon *float64
	if req.Lat != nil {
		lat, lon = &req.Lat.V, &req.Lon.V
	}
	entry, err := a.Store.Create(req.Text, time.Now(), req.Automated, lat, lon)
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
		writeErr(w, 400, "text exceeds 10000 characters")
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

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validDate(s string) bool { return dateRe.MatchString(s) }

func (a *API) verify(w http.ResponseWriter, r *http.Request) {
	res, err := a.Store.VerifyChain()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (a *API) listSeals(w http.ResponseWriter, r *http.Request) {
	seals, err := a.Store.ListSeals()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if seals == nil {
		seals = []*DaySeal{}
	}
	writeJSON(w, 200, map[string]any{"seals": seals})
}

func (a *API) getSeal(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, 400, "invalid date (expected YYYY-MM-DD)")
		return
	}
	ds, err := a.Store.GetSeal(date)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, 404, "seal not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ds)
}

func (a *API) getSealProof(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, 400, "invalid date (expected YYYY-MM-DD)")
		return
	}
	proof, err := a.Store.GetOTSProof(date)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, 404, "seal not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if proof == nil {
		writeErr(w, 404, "ots proof not yet available")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.opentimestamps.ots")
	w.Header().Set("Content-Disposition", `attachment; filename="`+date+`.ots"`)
	_, _ = w.Write(proof)
}

func (a *API) triggerSeal(w http.ResponseWriter, r *http.Request) {
	if !a.checkAPIKey(r) {
		writeErr(w, 401, "invalid or missing API key")
		return
	}
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, 400, "invalid date (expected YYYY-MM-DD)")
		return
	}
	tz := a.serverTZ()
	d, err := time.ParseInLocation("2006-01-02", date, tz)
	if err != nil {
		writeErr(w, 400, "invalid date")
		return
	}
	today := time.Now().In(tz)
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, tz)
	if !d.Before(todayStart) {
		writeErr(w, 400, "can only seal past days")
		return
	}
	ds, created, err := a.Store.SealDay(date)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if created {
		go a.submitOTS(date, ds.SealHash)
	}
	status := 200
	if created {
		status = 201
	}
	writeJSON(w, status, map[string]any{"seal": ds, "created": created})
}

// submitOTS is called async after a new seal is written. Failures are logged.
func (a *API) submitOTS(date string, sealHash []byte) {
	proof, err := SubmitOTS(sealHash)
	if err != nil {
		log.Printf("ots submit %s: %v", date, err)
		return
	}
	if err := a.Store.SetOTSProof(date, proof); err != nil {
		log.Printf("ots store %s: %v", date, err)
	}
}
