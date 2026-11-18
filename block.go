package main

// block.go — public user ID (NX123456), device codes (D1a2b3c4d) aur device block.
// Blocked device ko site par kuch nahi dikhta (GateHandler har request par check karta hai).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const cBlocks = "blocks"
const cDevs = "devs"

var reDevCode = regexp.MustCompile(`^D[0-9a-f]{8}$`)

type BlockedDev struct {
	Code string    `json:"code"`
	Note string    `json:"note"`
	T    time.Time `json:"t"`
	// Until: zero = hamesha block; warna us waqt auto-unblock
	Until time.Time `json:"until,omitempty"`
}

// DevCode: browser/device ke cid se chhota code (cid kabhi admin ko nahi dikhta).
func DevCode(cid string) string {
	sum := sha256.Sum256([]byte("dev:" + cid))
	return "D" + hex.EncodeToString(sum[:4])
}

func (h *Hub) newPubLocked() string {
	for {
		n, _ := strconv.ParseUint(randHex(4), 16, 32)
		p := fmt.Sprintf("NX%06d", n%900000+100000)
		if _, used := h.pubIdx[p]; !used {
			return p
		}
	}
}

func (h *Hub) assignPubLocked(u *User) {
	if u.Pub == "" {
		u.Pub = h.newPubLocked()
	}
	h.pubIdx[u.Pub] = u.ID
}

// initPubs: purane users ko bhi public ID de do (ek baar).
func (h *Hub) initPubs() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, u := range h.users {
		if u.Pub != "" {
			h.pubIdx[u.Pub] = u.ID
		}
	}
	for _, u := range h.users {
		if u.Pub == "" {
			h.assignPubLocked(u)
			h.put(cUsers, u.ID, u)
		}
	}
}

// NoteDevice: user kis-kis device se aaya (aakhri 8 yaad rehte hain).
func (h *Hub) NoteDevice(uid, code string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return
	}
	for _, d := range u.Dev {
		if d == code {
			return
		}
	}
	u.Dev = append(u.Dev, code)
	if len(u.Dev) > 8 {
		u.Dev = u.Dev[len(u.Dev)-8:]
	}
	h.put(cUsers, u.ID, u)
}

func (h *Hub) expireBlocksLocked(now time.Time) {
	for c, d := range h.devBl {
		if !d.Until.IsZero() && now.After(d.Until) {
			delete(h.devBl, c)
			h.del(cBlocks, c)
		}
	}
	for id, u := range h.users {
		if u.Blocked && !u.BlockedUntil.IsZero() && now.After(u.BlockedUntil) {
			h.setBlockedLocked(id, false, time.Time{})
		}
	}
}

func (h *Hub) blockDevLocked(code, note string) {
	if h.devBl[code] != nil {
		return
	}
	b := &BlockedDev{Code: code, Note: note, T: time.Now()}
	h.devBl[code] = b
	h.put(cBlocks, code, b)
}

func (h *Hub) unblockDevLocked(code string) bool {
	if h.devBl[code] == nil {
		return false
	}
	delete(h.devBl, code)
	h.del(cBlocks, code)
	return true
}

func (h *Hub) BlockDevice(code, note string) bool { return h.BlockDeviceUntil(code, note, time.Time{}) }

func (h *Hub) BlockDeviceUntil(code, note string, until time.Time) bool {
	code = strings.TrimSpace(code)
	if !reDevCode.MatchString(code) {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blockDevLocked(code, note)
	if d := h.devBl[code]; d != nil {
		d.Until = until
		h.put(cBlocks, code, d)
	}
	return true
}

func (h *Hub) UnblockDevice(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.unblockDevLocked(strings.TrimSpace(code))
}

func (h *Hub) DeviceBlocked(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.devBl[code] != nil
}

func (h *Hub) BlockedDevices() []*BlockedDev {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*BlockedDev, 0, len(h.devBl))
	for _, b := range h.devBl {
		c := *b
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.After(out[j].T) })
	return out
}

// DeviceByThread: 🧵 code se device code (guest = uska device, user = uska aakhri device).
func (h *Hub) DeviceByThread(code string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	who, ok := h.codes[code]
	if !ok {
		return ""
	}
	if strings.HasPrefix(who, "g:") {
		return DevCode(strings.TrimPrefix(who, "g:"))
	}
	if u := h.users[strings.TrimPrefix(who, "u:")]; u != nil && len(u.Dev) > 0 {
		return u.Dev[len(u.Dev)-1]
	}
	return ""
}

// ---- visitors: kaun kab aaya (login ho ya na ho) ----

type DevSeen struct {
	Code   string    `json:"code"`
	CID    string    `json:"cid"` // sirf server mein; admin ko kabhi nahi dikhta
	UA     string    `json:"ua"`
	UID    string    `json:"uid,omitempty"` // account se login kiya ho toh
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
	Visits int       `json:"visits"`
	saved  time.Time
}

func uaLabel(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "nxapp"):
		return "App"
	case strings.Contains(l, "android"):
		return "Android"
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipad"):
		return "iPhone/iPad"
	case strings.Contains(l, "windows"):
		return "Windows"
	case strings.Contains(l, "mac os"), strings.Contains(l, "macintosh"):
		return "Mac"
	case strings.Contains(l, "linux"):
		return "Linux"
	}
	return "Other"
}

// TouchDevice: har API request par (halka, disk par 10 minute mein ek baar likhta hai).
func (h *Hub) TouchDevice(cid, ua, uid string) {
	code := DevCode(cid)
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.seen[code]
	if d == nil {
		d = &DevSeen{Code: code, CID: cid, UA: uaLabel(ua), First: now, Visits: 1}
		h.seen[code] = d
	} else if now.Sub(d.Last) > 30*time.Minute {
		d.Visits++
	}
	if uid != "" {
		d.UID = uid
	}
	d.Last = now
	if d.saved.IsZero() || now.Sub(d.saved) > 10*time.Minute {
		d.saved = now
		h.put(cDevs, code, d)
	}
}

type VisitStats struct {
	Online, Today, Week, Total, Guests, Users int
}

func istMidnight(now time.Time) time.Time {
	ist := time.FixedZone("IST", 5*3600+1800)
	n := now.In(ist)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, ist)
}

func (h *Hub) VisitStats() VisitStats {
	now := time.Now()
	mid := istMidnight(now)
	h.mu.Lock()
	defer h.mu.Unlock()
	var s VisitStats
	s.Users = len(h.users)
	for _, d := range h.seen {
		s.Total++
		if d.UID == "" {
			s.Guests++
		}
		if now.Sub(d.Last) <= 5*time.Minute {
			s.Online++
		}
		if d.Last.After(mid) {
			s.Today++
		}
		if now.Sub(d.Last) <= 7*24*time.Hour {
			s.Week++
		}
	}
	return s
}

// Guests: jin devices se kabhi login nahi hua, sabse naye pehle.
func (h *Hub) Guests() []*DevSeen {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*DevSeen
	for _, d := range h.seen {
		if d.UID == "" {
			c := *d
			c.CID = ""
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// ThreadForDevice / ThreadForUser: us ko message bhejne ke liye 🧵 thread code (admin ka Reply yahin jayega).
func (h *Hub) ThreadForDevice(code string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.seen[code]
	if d == nil || d.CID == "" {
		return "", false
	}
	who := "g:" + d.CID
	t := ThreadCode(who)
	h.codes[t] = who
	return t, true
}

func (h *Hub) ThreadForUser(uid string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.users[uid] == nil {
		return "", false
	}
	who := "u:" + uid
	t := ThreadCode(who)
	h.codes[t] = who
	return t, true
}
