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

var reDevCode = regexp.MustCompile(`^D[0-9a-f]{8}$`)

type BlockedDev struct {
	Code string    `json:"code"`
	Note string    `json:"note"`
	T    time.Time `json:"t"`
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

func (h *Hub) BlockDevice(code, note string) bool {
	code = strings.TrimSpace(code)
	if !reDevCode.MatchString(code) {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blockDevLocked(code, note)
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
