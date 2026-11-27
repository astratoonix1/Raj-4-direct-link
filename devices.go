package main

// Premium device limit: ek premium account ek saath sirf N (default 2) device par stream kare.
// Device "active" tab tak gina jaata hai jab tak wo devWindow se zyada der idle na ho.
// Admin: /devlimit (haal), /devlimit <n>, /devlimit off.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	cDevLimitKey    = "devlim"
	defaultDevLimit = 2
	devWindow       = 15 * time.Minute
)

type devSlot struct {
	Last  time.Time
	Label string
}

type devState struct {
	Set   bool                           `json:"set"`
	N     int                            `json:"n"`
	slots map[string]map[string]*devSlot // uid -> device code -> slot (memory only)
}

func (h *Hub) loadDevLimit(b []byte) {
	var s struct {
		Set bool `json:"set"`
		N   int  `json:"n"`
	}
	if json.Unmarshal(b, &s) == nil {
		h.vip.dev.Set, h.vip.dev.N = s.Set, s.N
	}
}

func (h *Hub) devLimitLocked() int {
	if !h.vip.dev.Set {
		return defaultDevLimit
	}
	return h.vip.dev.N
}

// DevLimit: 0 = limit band.
func (h *Hub) DevLimit() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.devLimitLocked()
}

func (h *Hub) SetDevLimit(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n < 0 {
		n = 0
	}
	h.vip.dev.Set, h.vip.dev.N = true, n
	h.put(cSettings, cDevLimitKey, struct {
		Set bool `json:"set"`
		N   int  `json:"n"`
	}{true, n})
}

// StreamSlot: is device ko stream karne ki jagah mili? false = limit poori.
func (h *Hub) StreamSlot(uid, code, label string) bool {
	if uid == "" || code == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	lim := h.devLimitLocked()
	if lim <= 0 {
		return true
	}
	now := time.Now()
	if h.vip.dev.slots == nil {
		h.vip.dev.slots = map[string]map[string]*devSlot{}
	}
	m := h.vip.dev.slots[uid]
	if m == nil {
		m = map[string]*devSlot{}
		h.vip.dev.slots[uid] = m
	}
	for c, s := range m {
		if now.Sub(s.Last) > devWindow {
			delete(m, c)
		}
	}
	if s := m[code]; s != nil {
		s.Last = now
		return true
	}
	if len(m) >= lim {
		return false
	}
	m[code] = &devSlot{Last: now, Label: label}
	return true
}

type DevInfo struct {
	Label string `json:"label"`
	AgoS  int    `json:"ago_s"`
	This  bool   `json:"this"`
}

// DevicesOf: abhi active devices (premium page ke liye).
func (h *Hub) DevicesOf(uid, this string) (int, []DevInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	out := []DevInfo{}
	for c, s := range h.vip.dev.slots[uid] {
		if ago := now.Sub(s.Last); ago <= devWindow {
			out = append(out, DevInfo{Label: s.Label, AgoS: int(ago.Seconds()), This: c == this})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgoS < out[j].AgoS })
	return h.devLimitLocked(), out
}

// devLabel: "Android · Chrome" jaisa chhota naam.
func devLabel(ua string) string {
	l := strings.ToLower(ua)
	os := "Device"
	switch {
	case strings.Contains(l, "iphone"):
		os = "iPhone"
	case strings.Contains(l, "ipad"):
		os = "iPad"
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "windows"):
		os = "Windows PC"
	case strings.Contains(l, "mac os"), strings.Contains(l, "macintosh"):
		os = "Mac"
	case strings.Contains(l, "linux"):
		os = "Linux"
	}
	br := ""
	switch {
	case strings.Contains(l, "edg/"):
		br = "Edge"
	case strings.Contains(l, "opr/"), strings.Contains(l, "opera"):
		br = "Opera"
	case strings.Contains(l, "firefox"):
		br = "Firefox"
	case strings.Contains(l, "chrome"), strings.Contains(l, "crios"):
		br = "Chrome"
	case strings.Contains(l, "safari"):
		br = "Safari"
	case strings.Contains(l, "vlc"):
		br = "VLC"
	}
	if br == "" {
		return os
	}
	return os + " · " + br
}

// PayETA: pichhli approved payments ka median verify-time (minute). 0 = andaza nahi.
func (h *Hub) PayETA() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	var d []time.Duration
	for _, p := range h.pays {
		if p.Status == "approved" && !p.Done.IsZero() && p.Done.After(p.Created) {
			d = append(d, p.Done.Sub(p.Created))
		}
	}
	if len(d) < 3 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	m := int(d[len(d)/2].Minutes())
	if m < 1 {
		m = 1
	}
	return m
}

func (a *App) devLimitCommand(m *tgbotapi.Message, arg string) bool {
	if m.Command() != "devlimit" {
		return false
	}
	chat := m.Chat.ID
	arg = strings.ToLower(strings.TrimSpace(arg))
	switch {
	case arg == "off":
		a.hub.SetDevLimit(0)
	case arg != "":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > 20 {
			a.send(chat, "Aise likho: /devlimit <1-20> ya /devlimit off\nJaise: /devlimit 2")
			return true
		}
		a.hub.SetDevLimit(n)
	}
	n := a.hub.DevLimit()
	st := fmt.Sprintf("🟢 %d device ek saath", n)
	if n == 0 {
		st = "🔴 BAND (koi limit nahi)"
	}
	a.send(chat, fmt.Sprintf("📱 Premium device limit: %s\n\nSirf premium (paid/trial) users par lagta hai, stream/download par. Device 15 minute idle rahe to jagah khaali ho jaati hai. Admin par limit nahi.\n\n/devlimit <n> · /devlimit off", st))
	return true
}
