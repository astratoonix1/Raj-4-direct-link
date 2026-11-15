package main

// ad.go — home page ka chhota gol "ad" popup: image + text + link.
// Bot se set hota hai: /ad <image url> | text | link   (ya photo bhejo, caption: /ad text | link)

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const cAdKey = "ad"

type Ad struct {
	On      bool      `json:"on"`
	Img     string    `json:"img"` // https://... ya tg:<file_id>
	Text    string    `json:"text"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
}

func (h *Hub) Ad() Ad {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ad
}

func (h *Hub) SetAd(a Ad) {
	h.mu.Lock()
	defer h.mu.Unlock()
	a.Updated = time.Now()
	h.ad = a
	h.put(cSettings, cAdKey, a)
}

func (h *Hub) SetAdOn(on bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ad.Img == "" {
		return false
	}
	h.ad.On = on
	h.put(cSettings, cAdKey, h.ad)
	return true
}

func (h *Hub) DeleteAd() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ad = Ad{}
	h.put(cSettings, cAdKey, h.ad)
}

func isHTTPSURL(s string) bool {
	return strings.HasPrefix(s, "https://") && len(s) < 2000 && !strings.ContainsAny(s, " \r\n\"'<>")
}

// ParseAdArgs: "text | link" (photo wale form ke liye)
func ParseAdTextLink(s string) (text, link string, ok bool) {
	parts := strings.Split(s, "|")
	text = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		link = strings.TrimSpace(parts[1])
	}
	if link != "" && !isHTTPSURL(link) {
		return "", "", false
	}
	if r := []rune(text); len(r) > 240 {
		text = string(r[:240])
	}
	return text, link, true
}

// adPublic: site ke liye (sirf jab ad on ho)
func (a *Accounts) adPublic() map[string]any {
	ad := a.hub.Ad()
	if !ad.On || ad.Img == "" {
		return nil
	}
	img := ad.Img
	if strings.HasPrefix(img, "tg:") {
		img = "/api/ad/img?v=" + strconv.FormatInt(ad.Updated.Unix(), 10)
	}
	return map[string]any{"img": img, "text": ad.Text, "url": ad.URL, "v": ad.Updated.Unix()}
}

type adCache struct {
	mu   sync.Mutex
	id   string
	data []byte
	ct   string
}

func (a *Accounts) adImage(w http.ResponseWriter, r *http.Request) {
	ad := a.hub.Ad()
	if !ad.On || !strings.HasPrefix(ad.Img, "tg:") || a.AdFetch == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(ad.Img, "tg:")
	c := &a.adc
	c.mu.Lock()
	if c.id != id {
		c.mu.Unlock()
		data, ct, err := a.AdFetch(id)
		if err != nil || len(data) == 0 {
			http.Error(w, "image nahi mili", 502)
			return
		}
		c.mu.Lock()
		c.id, c.data, c.ct = id, data, ct
	}
	data, ct := c.data, c.ct
	c.mu.Unlock()
	if ct == "" || !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}
