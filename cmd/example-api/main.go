package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type counters struct {
	mu sync.Mutex
	m  map[string]int
}

func (c *counters) next(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key]++
	return c.m[key]
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	flag.Parse()

	retries := &counters{m: map[string]int{}}
	repeats := &counters{m: map[string]int{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": "veriflow-example-api",
			"version": 1,
		})
	})

	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"url": "http://" + r.Host + r.URL.RequestURI(),
		})
	})

	mux.HandleFunc("/api/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"data": map[string]any{
				"id":      "resource-001",
				"ownerId": body["ownerId"],
				"name":    body["name"],
				"status":  "Open",
			},
		})
	})

	mux.HandleFunc("/api/resources/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/resources/")
		if id == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "missing_id"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{
				"data": map[string]any{
					"id":            id,
					"status":        "Open",
					"tags":          []string{"alpha", "beta", "gamma"},
					"score":         42,
					"active":        true,
					"nullable":      nil,
					"createdAt":     "2026-06-01T12:00:00Z",
					"correlationId": r.Header.Get("X-Correlation-ID"),
				},
			})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		}
	})

	mux.HandleFunc("/api/echo/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"pathValue": strings.TrimPrefix(r.URL.Path, "/api/echo/"),
			"query":     r.URL.Query().Get("q"),
			"token":     r.Header.Get("X-Token"),
		})
	})

	mux.HandleFunc("/api/raw", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		writeJSON(w, http.StatusOK, map[string]any{"body": string(body), "length": len(body)})
	})

	mux.HandleFunc("/api/file", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		writeJSON(w, http.StatusOK, map[string]any{
			"length": len(body),
			"sha256": hex.EncodeToString(sum[:]),
		})
	})

	mux.HandleFunc("/api/json-file", func(w http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"received": body})
	})

	mux.HandleFunc("/api/form", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_form"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":  r.Form.Get("name"),
			"count": r.Form.Get("count"),
		})
	})

	mux.HandleFunc("/api/multipart", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_multipart", "message": err.Error()})
			return
		}
		file, header, err := r.FormFile("document")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_document"})
			return
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		writeJSON(w, http.StatusOK, map[string]any{
			"note":     r.FormValue("note"),
			"filename": header.Filename,
			"size":     len(body),
		})
	})

	mux.HandleFunc("/api/retry", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			key = "default"
		}
		attempt := retries.next(key)
		if attempt < 3 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"attempt": attempt, "status": "warming"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"attempt": attempt, "status": "ready"})
	})

	mux.HandleFunc("/api/repeat", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			key = "default"
		}
		seen := repeats.next(key)
		writeJSON(w, http.StatusOK, map[string]any{"seen": seen})
	})

	mux.HandleFunc("/api/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/health", http.StatusFound)
	})

	mux.HandleFunc("/api/text", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Pipeline", "ready")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pipeline-ok: hello world")
	})

	mux.HandleFunc("/api/session/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture-session", Path: "/", HttpOnly: true})
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/session/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		cookie, err := r.Cookie("session")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing_session"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session": cookie.Value})
	})

	mux.HandleFunc("/api/extraction", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Trace-ID", "trace-123")
		http.SetCookie(w, &http.Cookie{Name: "marker", Value: "cookie-456", Path: "/"})
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ticket=text-789")
	})

	mux.HandleFunc("/api/assertions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"string":   "hello pipeline",
			"number":   42,
			"boolean":  true,
			"nullable": nil,
			"items":    []any{"alpha", "beta", "gamma"},
			"objects": []any{
				map[string]any{"id": 1},
				map[string]any{"id": 2},
			},
			"minimumAge": 21,
			"users": []any{
				map[string]any{"id": 10, "name": "alice", "age": 30, "meta": map[string]any{"code": "A1"}},
				map[string]any{"id": 11, "name": "bob", "age": 19, "meta": map[string]any{"code": "B2"}},
			},
			"createdAt": "2026-06-01T12:00:00Z",
		})
	})

	mux.HandleFunc("/api/slow", func(w http.ResponseWriter, r *http.Request) {
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		if ms < 0 || ms > 2000 {
			ms = 0
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]any{"sleptMs": ms})
	})

	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("veriflow example API listening on http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		_, _ = fmt.Fprintln(w, `{"error":"encode_failed"}`)
	}
}
