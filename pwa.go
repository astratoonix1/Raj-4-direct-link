package main

// PWA: manifest, service worker aur icons (koi image file nahi chahiye, Go khud banata hai).

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strings"
	"sync"
)

var iconCache sync.Map // "192" / "512" / "m512" -> []byte

// drawIcon: gehra background + violet-cyan gola + safed play-triangle. maskable = poora square (system khud round karta hai).
func drawIcon(size int, maskable bool) []byte {
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	rad := s * 0.22
	// play triangle: andar ke safe zone mein (maskable ke liye ~60% ke andar)
	cx, cy := s*0.52, s*0.5
	h := s * 0.30
	circ := s * 0.33
	if maskable {
		h = s * 0.24
		circ = s * 0.27
	}
	w := h * 0.9
	ax, ay := cx-w/2, cy-h/2
	bx, by := cx-w/2, cy+h/2
	px, py := cx+w/2, cy
	inTri := func(x, y float64) bool {
		d := func(x1, y1, x2, y2 float64) float64 { return (x-x2)*(y1-y2) - (x1-x2)*(y-y2) }
		d1, d2, d3 := d(ax, ay, bx, by), d(bx, by, px, py), d(px, py, ax, ay)
		neg := d1 < 0 || d2 < 0 || d3 < 0
		pos := d1 > 0 || d2 > 0 || d3 > 0
		return !(neg && pos)
	}
	inRound := func(x, y float64) bool {
		if maskable {
			return true
		}
		dx := math.Max(math.Max(rad-x, x-(s-rad)), 0)
		dy := math.Max(math.Max(rad-y, y-(s-rad)), 0)
		return dx*dx+dy*dy <= rad*rad
	}
	const ss = 3 // supersampling: smooth kinare
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					fx, fy := float64(x)+(float64(i)+0.5)/ss, float64(y)+(float64(j)+0.5)/ss
					var c color.RGBA
					cov := 0.0
					if inRound(fx, fy) {
						cov = 1
						switch {
						case inTri(fx, fy):
							c = white
						case (fx-s/2)*(fx-s/2)+(fy-s/2)*(fy-s/2) <= circ*circ:
							t := (fx + fy) / (2 * s) // neon violet -> cyan (site ke splash jaisa)
							c = color.RGBA{uint8(150 - 110*t), uint8(70 + 150*t), uint8(255), 255}
						default:
							t := fy / s // halka gradient
							c = color.RGBA{uint8(11 + 14*t), uint8(11 + 6*t), uint8(15 + 22*t), 255}
						}
					}
					r += float64(c.R) * cov
					g += float64(c.G) * cov
					b += float64(c.B) * cov
					a += cov
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				img.SetRGBA(x, y, color.RGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a / n * 255)})
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func iconPNG(name string) []byte {
	if v, ok := iconCache.Load(name); ok {
		return v.([]byte)
	}
	var b []byte
	switch name {
	case "192":
		b = drawIcon(192, false)
	case "512":
		b = drawIcon(512, false)
	case "m512":
		b = drawIcon(512, true)
	default:
		return nil
	}
	iconCache.Store(name, b)
	return b
}

const swJS = `// NX service worker: sirf page-navigation ko sambhalta hai (offline hone par saaf message).
// API, video stream aur downloads ko bilkul nahi chhuta.
const OFFLINE = '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Offline</title><body style="margin:0;background:#0b0b0f;color:#f2f2f5;font-family:system-ui,sans-serif;display:flex;min-height:100vh;align-items:center;justify-content:center;text-align:center;padding:24px"><div><div style="font-size:3rem">📡</div><h2>Internet nahi hai</h2><p style="color:#9a9aa8">Connection check karo aur dobara try karo.</p><button onclick="location.reload()" style="background:#E50914;color:#fff;border:0;border-radius:6px;padding:12px 22px;font-size:1rem">Retry</button></div></body>';
self.addEventListener('install', function () { self.skipWaiting(); });
self.addEventListener('activate', function (e) { e.waitUntil(self.clients.claim()); });
self.addEventListener('fetch', function (e) {
  var r = e.request;
  if (r.mode !== 'navigate') return;
  e.respondWith(fetch(r).catch(function () {
    return new Response(OFFLINE, { status: 503, headers: { 'Content-Type': 'text/html; charset=utf-8' } });
  }));
});
`

func shortName(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return "NX"
	}
	if len(r) > 12 {
		r = r[:12]
	}
	return string(r)
}

func (a *Accounts) pwaHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(a.hub.cfg.SiteName)
		if name == "" {
			name = "NX"
		}
		m := map[string]any{
			"name": name, "short_name": shortName(name), "id": "/", "start_url": "/", "scope": "/",
			"display": "standalone", "orientation": "any",
			"background_color": "#0b0b0f", "theme_color": "#0b0b0f",
			"description": name + " - movies, shows and courses",
			"icons": []map[string]string{
				{"src": "/pwa-icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
				{"src": "/pwa-icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
				{"src": "/pwa-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
			},
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write([]byte(swJS))
	})
	for path, name := range map[string]string{"/pwa-icon-192.png": "192", "/pwa-icon-512.png": "512", "/pwa-maskable-512.png": "m512"} {
		name := name
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Write(iconPNG(name))
		})
	}
}

func isPWAPath(p string) bool {
	switch p {
	case "/manifest.webmanifest", "/sw.js", "/pwa-icon-192.png", "/pwa-icon-512.png", "/pwa-maskable-512.png":
		return true
	}
	return false
}
