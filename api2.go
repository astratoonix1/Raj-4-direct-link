package main

// api2.go — login / OTP / request / contact / updates / sync endpoints (std library only).

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OwnerInfo struct {
	Name     string `json:"name"`
	About    string `json:"about"`
	Photo    string `json:"photo"`
	Telegram string `json:"telegram"`
}

type Accounts struct {
	Gate  bool // true = bina login ke site nahi khulegi
	hub   *Hub
	cat   *Catalog
	tmdb  *TMDB
	owner OwnerInfo
}

const cookieName = "nx_s"

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *Accounts) ExtraConfig() map[string]any {
	return map[string]any{
		"accounts": a.hub.mail != nil && a.hub.mail.Enabled(),
		"gate":     a.gateOn(),
		"owner":    a.owner,
	}
}

func (a *Accounts) user(r *http.Request) *User {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	return a.hub.Auth(c.Value)
}

// who: logged-in user ya guest device id
func (a *Accounts) who(r *http.Request) (who string, u *User) {
	if u = a.user(r); u != nil {
		return "u:" + u.ID, u
	}
	if cid := r.Header.Get("X-Cid"); ValidCID(cid) {
		return "g:" + cid, nil
	}
	return "", nil
}

func fail(w http.ResponseWriter, err error) {
	var he *HubErr
	code, msg := 500, "Kuch gadbad hui, dobara try karo."
	if errors.As(err, &he) {
		code, msg = he.Code, he.Msg
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

func okJSON(w http.ResponseWriter, v map[string]any) {
	if v == nil {
		v = map[string]any{}
	}
	v["ok"] = true
	writeJSON(w, v)
}

// post: sirf POST + JSON + same-origin (CSRF se bachav)
func post(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		fail(w, herr(405, "POST chahiye"))
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			fail(w, herr(403, "Request band ki gayi."))
			return false
		}
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, herr(415, "JSON chahiye"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, herr(400, "Data galat hai."))
		return false
	}
	return true
}

func (a *Accounts) setCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r), MaxAge: maxAge})
}

func userJSON(u *User) map[string]any {
	return map[string]any{"name": u.Name, "email": u.Email}
}

func (a *Accounts) Register(mux *http.ServeMux) {
	h := a.hub
	type emailReq struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Pass  string `json:"password"`
		OTP   string `json:"otp"`
	}
	mux.HandleFunc("/api/auth/signup", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 4096, &b) {
			return
		}
		if err := h.Signup(b.Name, b.Email, b.Pass, clientIP(r)); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/auth/resend", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 4096, &b) {
			return
		}
		if err := h.ResendSignup(b.Email); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 4096, &b) {
			return
		}
		u, tok, err := h.VerifySignup(b.Email, b.OTP)
		if err != nil {
			fail(w, err)
			return
		}
		a.setCookie(w, r, tok, 60*86400)
		okJSON(w, map[string]any{"user": userJSON(u)})
	})
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 4096, &b) {
			return
		}
		u, tok, err := h.Login(b.Email, b.Pass, clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		a.setCookie(w, r, tok, 60*86400)
		okJSON(w, map[string]any{"user": userJSON(u)})
	})
	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if !post(w, r, 1024, &b) {
			return
		}
		if c, err := r.Cookie(cookieName); err == nil {
			h.Logout(c.Value)
		}
		a.setCookie(w, r, "", -1)
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/auth/forgot", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 4096, &b) {
			return
		}
		if err := h.Forgot(b.Email, clientIP(r)); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/auth/reset", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 4096, &b) {
			return
		}
		if err := h.Reset(b.Email, b.OTP, b.Pass); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		u := a.user(r)
		if u == nil {
			writeJSON(w, map[string]any{"user": nil})
			return
		}
		writeJSON(w, map[string]any{"user": userJSON(u), "sync": h.GetSync(u.ID)})
	})
	mux.HandleFunc("/api/me/name", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 2048, &b) {
			return
		}
		u := a.user(r)
		if u == nil {
			fail(w, herr(401, "Pehle Sign In karo."))
			return
		}
		if err := h.SetName(u.ID, b.Name); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/me/email", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 2048, &b) {
			return
		}
		u := a.user(r)
		if u == nil {
			fail(w, herr(401, "Pehle Sign In karo."))
			return
		}
		if err := h.StartEmailChange(u.ID, b.Email, b.Pass); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/me/email/confirm", func(w http.ResponseWriter, r *http.Request) {
		var b emailReq
		if !post(w, r, 2048, &b) {
			return
		}
		u := a.user(r)
		if u == nil {
			fail(w, herr(401, "Pehle Sign In karo."))
			return
		}
		em, err := h.ConfirmEmailChange(u.ID, b.OTP)
		if err != nil {
			fail(w, err)
			return
		}
		okJSON(w, map[string]any{"email": em})
	})
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			List  json.RawMessage `json:"list"`
			Likes json.RawMessage `json:"likes"`
			CW    json.RawMessage `json:"cw"`
		}
		if !post(w, r, 300000, &b) {
			return
		}
		u := a.user(r)
		if u == nil {
			fail(w, herr(401, "Pehle Sign In karo."))
			return
		}
		ver, err := h.PutSync(u.ID, b.List, b.Likes, b.CW)
		if err != nil {
			fail(w, err)
			return
		}
		okJSON(w, map[string]any{"ver": ver})
	})

	// ---- request movie / series ----
	mux.HandleFunc("/api/request", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Year int    `json:"year"`
			Note string `json:"note"`
		}
		if !post(w, r, 4096, &b) {
			return
		}
		who, u := a.who(r)
		if who == "" {
			fail(w, herr(400, "Browser ki pehchaan nahi mili, page refresh karo."))
			return
		}
		name, email := "Guest", ""
		if u != nil {
			name, email = u.Name, u.Email
		}
		// pehle se site par hai?
		nn := normText(oneLine(b.Name, 100))
		for _, c := range a.cat.Search(b.Name) {
			if nn != "" && normText(c.Title) == nn {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(409)
				json.NewEncoder(w).Encode(map[string]any{"error": "Ye to site par pehle se hai!", "have": c.Key})
				return
			}
		}
		req, err := h.AddRequest(who, name, email, b.Kind, b.Name, b.Year, b.Note, clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		okJSON(w, map[string]any{"id": req.ID})
	})
	mux.HandleFunc("/api/suggest", func(w http.ResponseWriter, r *http.Request) {
		q := oneLine(r.URL.Query().Get("q"), 80)
		out := map[string]any{"have": []Card{}, "tmdb": []Suggestion{}}
		if len([]rune(q)) < 2 {
			writeJSON(w, out)
			return
		}
		if !h.rl.allow("sug:"+clientIP(r), 40, time.Minute) {
			fail(w, herr(429, "Dheere dheere type karo."))
			return
		}
		have := a.cat.Search(q)
		if len(have) > 4 {
			have = have[:4]
		}
		out["have"] = have
		if a.tmdb.Enabled() {
			ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			defer cancel()
			if s, err := a.tmdb.Suggest(ctx, q); err == nil {
				out["tmdb"] = s
			}
		}
		writeJSON(w, out)
	})

	// ---- owner message + inbox ----
	mux.HandleFunc("/api/contact", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name string `json:"name"`
			Text string `json:"text"`
		}
		if !post(w, r, 4096, &b) {
			return
		}
		who, u := a.who(r)
		if who == "" {
			fail(w, herr(400, "Browser ki pehchaan nahi mili, page refresh karo."))
			return
		}
		name, email := b.Name, ""
		if u != nil {
			name, email = u.Name, u.Email
		}
		if _, err := h.UserMessage(who, name, email, b.Text, clientIP(r)); err != nil {
			fail(w, err)
			return
		}
		okJSON(w, nil)
	})
	mux.HandleFunc("/api/inbox", func(w http.ResponseWriter, r *http.Request) {
		who, _ := a.who(r)
		out := map[string]any{"updates": h.Updates(30), "thread": []*Msg{}, "requests": []*Request{}}
		if who != "" {
			out["thread"] = h.Thread(who)
			rs := h.RequestsOf(who)
			for _, q := range rs {
				q.Who, q.Email = "", ""
			}
			out["requests"] = rs
		}
		writeJSON(w, out)
	})
}

func (a *Accounts) gateOn() bool { return a.Gate && a.hub.mail != nil && a.hub.mail.Enabled() }

// GateHandler: Gate on ho toh login ke bina sirf login wale pages / APIs khulte hain.
func (a *Accounts) GateHandler(next http.Handler) http.Handler {
	open := func(p string) bool {
		switch p {
		case "/", "/healthz", "/api/config", "/api/me", "/api/contact", "/api/inbox":
			return true
		}
		return strings.HasPrefix(p, "/api/auth/")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.gateOn() || open(r.URL.Path) || a.user(r) != nil {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/w/"):
			http.Redirect(w, r, "/?next="+url.QueryEscape(r.URL.Path)+"#/login", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"login"}`))
		default:
			http.Error(w, "Pehle login karo.", http.StatusUnauthorized)
		}
	})
}
