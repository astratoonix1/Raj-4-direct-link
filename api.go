package main

// api.go — site ke JSON endpoints (std library only).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type API struct {
	cat      *Catalog
	siteName string
	logoURL  string
	thumbURL string
	defQ     string
	extra    func() map[string]any // accounts / owner jaisi extra config
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]any{"site": a.siteName, "logo": a.logoURL, "thumb": a.thumbURL, "defq": a.defQ}
		if a.extra != nil {
			for k, v := range a.extra() {
				m[k] = v
			}
		}
		writeJSON(w, m)
	})
	mux.HandleFunc("/api/home", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.cat.Home())
	})
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		writeJSON(w, a.cat.Browse(q.Get("cat"), q.Get("genre"), q.Get("sort"), page))
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"items": a.cat.Search(r.URL.Query().Get("q"))})
	})
	mux.HandleFunc("/api/title/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/title/"), "/")
		tv := a.cat.TitleView(key)
		if tv == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, tv)
	})
	mux.HandleFunc("/api/file/", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/file/"), "/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		fv := a.cat.FileView(id)
		if fv == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, fv)
	})
	mux.HandleFunc("/api/view/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if id, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/view/"), "/")); err == nil {
			a.cat.AddView(id)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
