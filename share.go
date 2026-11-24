package main

// Share card: /share/<title key> — WhatsApp/Telegram/Facebook link preview (poster + naam) banata hai,
// phir browser ko site ke title page par bhej deta hai.

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
)

func shareOrigin(r *http.Request) string {
	sch := "http"
	if isHTTPS(r) {
		sch = "https"
	}
	return sch + "://" + r.Host
}

func absURL(origin, u string) string {
	u = strings.TrimSpace(u)
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "http://"):
		return u
	case strings.HasPrefix(u, "/"):
		return origin + u
	}
	return ""
}

func clip(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

func (a *API) share(w http.ResponseWriter, r *http.Request) {
	key := strings.Trim(strings.TrimPrefix(r.URL.Path, "/share/"), "/")
	site := a.siteName
	if site == "" {
		site = "AstraToonix"
	}
	origin := shareOrigin(r)
	t := a.cat.Title(key)
	if key == "" || t == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	title := t.Title
	if t.Year > 0 {
		title = fmt.Sprintf("%s (%d)", t.Title, t.Year)
	}
	desc := clip(t.Overview, 200)
	if desc == "" {
		desc = "Watch " + t.Title + " on " + site
	}
	img := absURL(origin, t.Poster)
	if img == "" {
		img = absURL(origin, t.Backdrop)
	}
	if img == "" {
		img = absURL(origin, a.thumbURL)
	}
	if img == "" {
		img = origin + "/pwa-icon-512.png"
	}
	page := origin + "/share/" + url.PathEscape(key)
	dest := "/#/t/" + url.PathEscape(key)
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%[1]s — %[2]s</title>
<meta name="description" content="%[3]s">
<meta property="og:site_name" content="%[2]s">
<meta property="og:type" content="website">
<meta property="og:title" content="%[1]s — %[2]s">
<meta property="og:description" content="%[3]s">
<meta property="og:image" content="%[4]s">
<meta property="og:url" content="%[5]s">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="%[1]s — %[2]s">
<meta name="twitter:image" content="%[4]s">
<meta http-equiv="refresh" content="0;url=%[6]s">
<style>body{background:#0b0b0f;color:#fff;font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0}a{color:#22d3ee}</style>
</head><body><p>Opening <b>%[1]s</b>… <a href="%[6]s">Click here</a></p>
<script>location.replace(%[7]q)</script></body></html>`,
		e(title), e(site), e(desc), e(img), e(page), e(dest), dest)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(b.String()))
}
