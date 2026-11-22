package main

// vip.go — Adsterra ads (admin/VIP ko nahi dikhte), VIP / Blue Tick, quality lock, subtitles.
//
//   • Ads code settings mein save hota hai (doc "ads"); site ko sirf free users ke liye diya jata hai.
//   • Status: admin (env ADMIN_EMAILS) > vip (premium chalu) > free.  Blue Tick = admin / vip / Verified.
//   • Quality lock: free = 480p tak; 720p, 1080p, 4K sirf VIP/admin (server /s/ aur /d/ par 402).
//   • Subtitles: admin .srt/.vtt ka https link attach karta hai, server VTT bana ke deta hai.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	cAdsKey   = "ads"
	cSubsKey  = "subs"
	cQLockKey = "qlock"
	// quality rank: 480p = 2, 720p = 3, 1080p = 4, 4K = 5
	lockFromRank = 3
	maxAdCode    = 8000
)

type AdsCfg struct {
	On      bool   `json:"on"`
	Banner  string `json:"banner"` // Adsterra banner code (script / iframe)
	Pop     string `json:"pop"`    // popunder / social bar script
	Updated int64  `json:"updated"`
}

type SubTrack struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

type vipState struct {
	ads    AdsCfg
	subs   map[int][]SubTrack
	qlock  bool
	aiOff  bool // AI support band
	idxOff bool // auto-index band
}

// loadVipSetting: Hub load ke time settings docs se (hub.go call karta hai)
func (h *Hub) loadVipSetting(id string, b []byte) {
	if strings.HasPrefix(id, uvPrefix) {
		h.loadUV(id, b)
		return
	}
	switch id {
	case cAdsKey:
		json.Unmarshal(b, &h.vip.ads)
	case cSubsKey:
		var s struct {
			M map[string][]SubTrack `json:"m"`
		}
		if json.Unmarshal(b, &s) == nil {
			h.vip.subs = map[int][]SubTrack{}
			for k, v := range s.M {
				if n, err := strconv.Atoi(k); err == nil {
					h.vip.subs[n] = v
				}
			}
		}
	case "idx":
		var s struct {
			Off bool `json:"off"`
		}
		if json.Unmarshal(b, &s) == nil {
			h.vip.idxOff = s.Off
		}
	case "ai":
		var s struct {
			Off bool `json:"off"`
		}
		if json.Unmarshal(b, &s) == nil {
			h.vip.aiOff = s.Off
		}
	case cQLockKey:
		var s struct {
			On bool `json:"on"`
		}
		if json.Unmarshal(b, &s) == nil {
			h.vip.qlock = s.On
		}
	}
}

func (h *Hub) Ads() AdsCfg {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vip.ads
}

func (h *Hub) SetAdsCode(kind, code string) error {
	code = strings.TrimSpace(code)
	if len(code) > maxAdCode {
		return herr(400, "Code bahut lamba hai (max %d characters).", maxAdCode)
	}
	if code != "" && !strings.Contains(strings.ToLower(code), "<script") && !strings.Contains(strings.ToLower(code), "<iframe") {
		return herr(400, "Ye ad code nahi lag raha. Adsterra ka poora <script>…</script> code paste karo.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch kind {
	case "banner":
		h.vip.ads.Banner = code
	case "pop":
		h.vip.ads.Pop = code
	case "all":
		h.vip.ads.Banner, h.vip.ads.Pop = "", ""
	default:
		return herr(400, "banner ya pop likho.")
	}
	h.vip.ads.Updated = time.Now().Unix()
	h.put(cSettings, cAdsKey, h.vip.ads)
	return nil
}

func (h *Hub) SetAdsOn(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vip.ads.On = on
	h.vip.ads.Updated = time.Now().Unix()
	h.put(cSettings, cAdsKey, h.vip.ads)
}

func (h *Hub) QualityLock() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vip.qlock
}

func (h *Hub) SetQualityLock(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vip.qlock = on
	h.put(cSettings, cQLockKey, map[string]bool{"on": on})
}

// ---------- VIP / Blue Tick ----------

// SetVIP: forever=true → 100 saal ka premium; false → hata do
func (h *Hub) SetVIP(uid string, on bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return false
	}
	if on {
		u.PremiumUntil = time.Now().AddDate(100, 0, 0)
		h.notifyUserLocked(uid, "👑 You are now a VIP member! All qualities (720p, 1080p, 4K) are unlocked and ads are off.")
	} else {
		u.PremiumUntil = time.Time{}
	}
	h.put(cUsers, u.ID, u)
	return true
}

func (h *Hub) SetVerified(uid string, on bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return false
	}
	u.Verified = on
	h.put(cUsers, u.ID, u)
	return true
}

func (a *Accounts) isAdmin(u *User) bool {
	return u != nil && a.Admins != nil && a.Admins[strings.ToLower(u.Email)]
}

// isVIP: admin ya premium chalu
func (a *Accounts) isVIP(u *User) bool {
	return u != nil && (a.isAdmin(u) || a.hub.PremiumActive(u.ID))
}

// status: "admin" | "vip" | "free"
func (a *Accounts) status(u *User) string {
	switch {
	case u == nil:
		return "free"
	case a.isAdmin(u):
		return "admin"
	case a.hub.PremiumActive(u.ID):
		return "vip"
	}
	return "free"
}

func (a *Accounts) blueTick(u *User) bool {
	return u != nil && (a.isAdmin(u) || u.Verified || a.hub.PremiumActive(u.ID))
}

// qualityLockOn: premium system chalu hai AUR lock on hai
func (a *Accounts) qualityLockOn() bool { return a.premiumOn() && a.hub.QualityLock() }

// adsPublic: free (guest ya free user) ko hi ad code milta hai
func (a *Accounts) adsPublic(u *User) map[string]any {
	if a.isVIP(u) {
		return nil
	}
	ads := a.hub.Ads()
	if !ads.On || (ads.Banner == "" && ads.Pop == "") {
		return nil
	}
	return map[string]any{"banner": ads.Banner, "pop": ads.Pop, "v": ads.Updated}
}

// lockedForUser: ye file is user ke liye VIP-only quality mein hai?
func (a *Accounts) lockedForUser(id int, u *User) bool {
	if !a.qualityLockOn() || a.isVIP(u) {
		return false
	}
	f := a.cat.File(id)
	return f != nil && f.QRank >= lockFromRank
}

// ---------- subtitles ----------

func (h *Hub) SubsOf(id int) []SubTrack {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]SubTrack(nil), h.vip.subs[id]...)
}

func (h *Hub) saveSubsLocked() {
	m := map[string][]SubTrack{}
	for k, v := range h.vip.subs {
		if len(v) > 0 {
			m[strconv.Itoa(k)] = v
		}
	}
	h.put(cSettings, cSubsKey, map[string]any{"m": m})
}

func (h *Hub) AddSub(id int, lang, url string) error {
	lang = strings.TrimSpace(lang)
	if r := []rune(lang); len(r) == 0 || len(r) > 24 {
		return herr(400, "Language ka naam 1-24 characters ka likho (jaise English).")
	}
	if !isHTTPSURL(url) {
		return herr(400, "Subtitle ka https:// link do (.srt ya .vtt).")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.vip.subs == nil {
		h.vip.subs = map[int][]SubTrack{}
	}
	list := h.vip.subs[id]
	for i := range list {
		if strings.EqualFold(list[i].Lang, lang) {
			list[i].URL = url
			h.saveSubsLocked()
			return nil
		}
	}
	if len(list) >= 12 {
		return herr(400, "Ek file par max 12 subtitles.")
	}
	h.vip.subs[id] = append(list, SubTrack{Lang: lang, URL: url})
	h.saveSubsLocked()
	return nil
}

func (h *Hub) DelSub(id, n int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.vip.subs[id]
	if n == 0 { // saare
		if len(list) == 0 {
			return false
		}
		delete(h.vip.subs, id)
	} else {
		if n < 1 || n > len(list) {
			return false
		}
		h.vip.subs[id] = append(list[:n-1:n-1], list[n:]...)
	}
	h.saveSubsLocked()
	return true
}

var (
	reSrtTime = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)
	reTags    = regexp.MustCompile(`(?i)</?(script|iframe|style)[^>]*>`)
)

// SRTtoVTT: .srt ko WebVTT banata hai (VTT ho toh waise hi, bas safai)
func SRTtoVTT(in string) string {
	in = strings.TrimPrefix(in, "\xef\xbb\xbf")
	in = strings.ReplaceAll(strings.ReplaceAll(in, "\r\n", "\n"), "\r", "\n")
	in = reTags.ReplaceAllString(in, "")
	if strings.HasPrefix(strings.TrimSpace(in), "WEBVTT") {
		return in
	}
	return "WEBVTT\n\n" + reSrtTime.ReplaceAllString(in, "$1.$2")
}

type subCache struct {
	mu sync.Mutex
	m  map[string]string
}

var subc = subCache{m: map[string]string{}}

var subHTTP = &http.Client{Timeout: 10 * time.Second}

func fetchVTT(url string) (string, error) {
	subc.mu.Lock()
	if s, ok := subc.m[url]; ok {
		subc.mu.Unlock()
		return s, nil
	}
	subc.mu.Unlock()
	resp, err := subHTTP.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	s := SRTtoVTT(string(b))
	subc.mu.Lock()
	if len(subc.m) > 60 {
		subc.m = map[string]string{}
	}
	subc.m[url] = s
	subc.mu.Unlock()
	return s, nil
}

// /api/subs/<fileID>        → [{lang, src}]
// /api/subs/<fileID>/<n>.vtt → WebVTT
func (a *Accounts) subsHandler(w http.ResponseWriter, r *http.Request) {
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/subs/"), "/")
	parts := strings.Split(p, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	list := a.hub.SubsOf(id)
	if len(parts) == 1 {
		out := []map[string]any{}
		for i, s := range list {
			out = append(out, map[string]any{"lang": s.Lang, "src": fmt.Sprintf("/api/subs/%d/%d.vtt", id, i+1)})
		}
		writeJSON(w, map[string]any{"subs": out})
		return
	}
	n, err := strconv.Atoi(strings.TrimSuffix(parts[1], ".vtt"))
	if err != nil || n < 1 || n > len(list) {
		http.NotFound(w, r)
		return
	}
	vtt, err := fetchVTT(list[n-1].URL)
	if err != nil {
		http.Error(w, "subtitle not available", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.Write([]byte(vtt))
}

// ---------- admin bot commands ----------

func (a *App) vipCommand(m *tgbotapi.Message, arg string) bool {
	chat := m.Chat.ID
	user := func() (*User, bool) {
		var u *User
		if arg == "" {
			u = a.threadUser(m)
		} else {
			u = a.hub.FindUser(strings.Fields(arg)[0])
		}
		if u == nil {
			a.send(chat, "Aise likho: /"+m.Command()+" <email ya NX ID>\n(ya user ke 🧵 message ko Reply karke sirf /"+m.Command()+")")
			return nil, false
		}
		return u, true
	}
	switch m.Command() {
	case "ads":
		ads := a.hub.Ads()
		st := func(s string) string {
			if s == "" {
				return "—  (set nahi)"
			}
			return fmt.Sprintf("✅ set (%d chars)", len(s))
		}
		on := "🔴 OFF"
		if ads.On {
			on = "🟢 ON"
		}
		a.send(chat, "📢 Adsterra ads: "+on+"\n\n🖼 Banner: "+st(ads.Banner)+"\n🪟 Popunder/Social bar: "+st(ads.Pop)+
			"\n\nAdmin aur VIP ko ads kabhi nahi dikhte.\n\n"+
			"/adsbanner <code> — banner code\n/adspop <code> — popunder / social bar code\n/adson  /adsoff — chalu / band\n/adsclear [banner|pop] — code hatao")
	case "adsbanner", "adspop":
		kind := "banner"
		if m.Command() == "adspop" {
			kind = "pop"
		}
		if err := a.hub.SetAdsCode(kind, m.CommandArguments()); err != nil {
			a.send(chat, "❌ "+err.Error()+"\n\nAise likho:\n/"+m.Command()+" <script src=\"...\"></script>")
			return true
		}
		a.send(chat, "✅ Code save ho gaya. Ads chalu karne ke liye /adson")
	case "adson", "adsoff":
		a.hub.SetAdsOn(m.Command() == "adson")
		if m.Command() == "adson" {
			a.send(chat, "🟢 Ads ON (sirf free users aur guests ko).")
		} else {
			a.send(chat, "🔴 Ads OFF.")
		}
	case "adsclear":
		k := strings.ToLower(arg)
		if k == "" {
			k = "all"
		}
		if err := a.hub.SetAdsCode(k, ""); err != nil {
			a.send(chat, "❌ "+err.Error())
			return true
		}
		a.send(chat, "🗑 Ad code hata diya ("+k+").")
	case "qualitylock":
		switch strings.ToLower(arg) {
		case "on":
			a.hub.SetQualityLock(true)
		case "off":
			a.hub.SetQualityLock(false)
		default:
			s := "🔴 OFF"
			if a.hub.QualityLock() {
				s = "🟢 ON"
			}
			a.send(chat, "🔒 Quality lock: "+s+"\nON ho toh free users sirf 480p dekh sakte hain; 720p/1080p/4K sirf VIP.\n(Ye tabhi kaam karta hai jab /premiumon ho.)\n\n/qualitylock on  ya  /qualitylock off")
			return true
		}
		note := ""
		if !a.acc.premiumOn() {
			note = "\n⚠️ Premium system abhi BAND hai, isliye lock tab tak asar nahi karega jab tak /premiumon na ho."
		}
		a.send(chat, "🔒 Quality lock: "+strings.ToUpper(arg)+note)
	case "vip":
		if u, ok := user(); ok {
			a.hub.SetVIP(u.ID, true)
			a.send(chat, "👑 VIP + 🔵 Blue Tick diya: "+u.Email+" ("+u.Pub+")\nHamesha ke liye. Hatane ke liye /unvip")
		}
	case "unvip":
		if u, ok := user(); ok {
			a.hub.SetVIP(u.ID, false)
			a.send(chat, "VIP hata diya: "+u.Email)
		}
	case "verify":
		if u, ok := user(); ok {
			a.hub.SetVerified(u.ID, true)
			a.send(chat, "🔵 Blue Tick diya: "+u.Email+" ("+u.Pub+")\n(VIP nahi diya — sirf tick.)")
		}
	case "unverify":
		if u, ok := user(); ok {
			a.hub.SetVerified(u.ID, false)
			a.send(chat, "Blue Tick hata diya: "+u.Email+"\n(Agar VIP chalu hai toh tick VIP se dikhega.)")
		}
	case "addsub":
		f := strings.Fields(arg)
		if len(f) != 3 {
			a.send(chat, "Aise likho:\n/addsub <file id> <language> <https link>\nJaise: /addsub 1234 English https://example.com/en.srt\n\n(file id = site ke /w/1234 wala number)")
			return true
		}
		id, _ := strconv.Atoi(f[0])
		if a.cat.File(id) == nil {
			a.send(chat, "❌ Ye file id nahi mili.")
			return true
		}
		if err := a.hub.AddSub(id, f[1], f[2]); err != nil {
			a.send(chat, "❌ "+err.Error())
			return true
		}
		a.send(chat, fmt.Sprintf("✅ %s subtitle add ho gaya (file %d).", f[1], id))
	case "sublist":
		id, _ := strconv.Atoi(strings.TrimSpace(arg))
		list := a.hub.SubsOf(id)
		if len(list) == 0 {
			a.send(chat, "Is file par koi subtitle nahi. /addsub <file id> <language> <link>")
			return true
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "💬 File %d ke subtitles:\n", id)
		for i, s := range list {
			fmt.Fprintf(&sb, "%d. %s — %s\n", i+1, s.Lang, s.URL)
		}
		sb.WriteString("\nHatane ke liye: /subdel " + strconv.Itoa(id) + " <number>  (0 = saare)")
		a.send(chat, sb.String())
	case "subdel":
		f := strings.Fields(arg)
		if len(f) != 2 {
			a.send(chat, "Aise likho: /subdel <file id> <number>   (0 = saare)")
			return true
		}
		id, _ := strconv.Atoi(f[0])
		n, _ := strconv.Atoi(f[1])
		if a.hub.DelSub(id, n) {
			a.send(chat, "🗑 Subtitle hata diya.")
		} else {
			a.send(chat, "❌ Nahi mila.")
		}
	case "ai", "aion", "aioff":
		switch m.Command() {
		case "aion":
			a.hub.SetAIEnabled(true)
		case "aioff":
			a.hub.SetAIEnabled(false)
		}
		on := "🔴 OFF"
		if a.hub.AIEnabled() {
			on = "🟢 ON"
		}
		key := "✅ GEMINI_API_KEY laga hai"
		if a.acc == nil || a.acc.Gemini == nil {
			key = "❌ GEMINI_API_KEY nahi laga (AI kaam nahi karega)"
		}
		a.send(chat, "🤖 AI assistant (Contact Owner chat): "+on+"\n"+key+"\n\nUser message bhejta hai to AI jawab deta hai. Tum Reply karo to us thread mein AI 20 minute chup rehta hai.\n/aion  /aioff")
	case "vips":
		var out []string
		for _, u := range a.hub.ActiveSubs() {
			out = append(out, fmt.Sprintf("👑 %s (%s) — till %s", u.Email, u.Pub, u.PremiumUntil.Format("02 Jan 2006")))
		}
		for _, u := range a.hub.VerifiedUsers() {
			out = append(out, fmt.Sprintf("🔵 %s (%s)", u.Email, u.Pub))
		}
		sort.Strings(out)
		if len(out) == 0 {
			a.send(chat, "Abhi koi VIP / Blue Tick user nahi.")
			return true
		}
		a.send(chat, "VIP / Blue Tick users:\n\n"+strings.Join(out, "\n"))
	default:
		return false
	}
	return true
}

func (h *Hub) VerifiedUsers() []*User {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*User
	for _, u := range h.users {
		if u.Verified {
			c := *u
			c.List, c.Likes, c.CW = nil, nil, nil
			out = append(out, &c)
		}
	}
	return out
}
