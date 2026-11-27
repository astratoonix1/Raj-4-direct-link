package main

// Premium free trial: har account ko (aur har device ko) sirf ek baar, kuch din ka premium.
// Admin: /trial (haal), /trial on|off, /trial <din>.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const cTrialKey = "trial"

type trialState struct {
	Off   bool            `json:"off"`
	Days  int             `json:"days"`
	Devs  map[string]bool `json:"devs"`
	Count int             `json:"count"`
}

func (h *Hub) loadTrial(b []byte) {
	var s trialState
	if json.Unmarshal(b, &s) == nil {
		if s.Devs == nil {
			s.Devs = map[string]bool{}
		}
		h.vip.trial = s
	}
}

func (h *Hub) saveTrialLocked() { h.put(cSettings, cTrialKey, h.vip.trial) }

func trialDays(s *trialState) int {
	if s.Days < 1 || s.Days > 30 {
		return 1
	}
	return s.Days
}

// TrialInfo: trial chalu hai? kitne din ka? ab tak kitne ne liya?
func (h *Hub) TrialInfo() (on bool, days, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.vip.trial.Off, trialDays(&h.vip.trial), h.vip.trial.Count
}

func (h *Hub) SetTrial(on bool, days int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vip.trial.Off = !on
	if days > 0 {
		h.vip.trial.Days = days
	}
	h.saveTrialLocked()
}

// TrialEligible: is user ko (is device se) trial mil sakta hai?
func (h *Hub) TrialEligible(uid, dev string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	return u != nil && h.trialOKLocked(u, dev) == ""
}

// trialOKLocked: "" = theek, warna wajah.
func (h *Hub) trialOKLocked(u *User, dev string) string {
	if h.vip.trial.Off {
		return "The free trial is not available right now."
	}
	if u.TrialUsed {
		return "You have already used your free trial."
	}
	if u.premiumActive(time.Now()) {
		return "Your Premium is already active."
	}
	if dev == "" {
		return "We could not identify your device. Please reload the page and try again."
	}
	if h.vip.trial.Devs[dev] {
		return "A free trial has already been used on this device."
	}
	for _, d := range u.Dev {
		if h.vip.trial.Devs[d] {
			return "A free trial has already been used on this device."
		}
	}
	return ""
}

func (h *Hub) ClaimTrial(uid, dev string) (time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return time.Time{}, herr(404, "User not found.")
	}
	if why := h.trialOKLocked(u, dev); why != "" {
		return time.Time{}, herr(403, "%s", why)
	}
	days := trialDays(&h.vip.trial)
	u.TrialUsed = true
	u.PremiumUntil = time.Now().Add(time.Duration(days) * 24 * time.Hour)
	h.put(cUsers, u.ID, u)
	if h.vip.trial.Devs == nil {
		h.vip.trial.Devs = map[string]bool{}
	}
	h.vip.trial.Devs[dev] = true
	for _, d := range u.Dev {
		h.vip.trial.Devs[d] = true
	}
	h.vip.trial.Count++
	h.saveTrialLocked()
	h.notifyUserLocked(uid, fmt.Sprintf("🎁 Your free trial has started! Premium is active until %s. Enjoy — if you like it, pick a plan any time.", u.PremiumUntil.Format("02 Jan, 03:04 PM")))
	return u.PremiumUntil, nil
}

// reqDevCode: request ke device ID (X-Cid / cookie) se device code.
func reqDevCode(r *http.Request) string {
	cid := r.Header.Get("X-Cid")
	if !ValidCID(cid) {
		cid = ""
		if c, err := r.Cookie(devCookie); err == nil && ValidCID(c.Value) {
			cid = c.Value
		}
	}
	if cid == "" {
		return ""
	}
	return DevCode(cid)
}

func (a *App) trialCommand(m *tgbotapi.Message, arg string) bool {
	if m.Command() != "trial" {
		return false
	}
	chat := m.Chat.ID
	arg = strings.ToLower(strings.TrimSpace(arg))
	switch {
	case arg == "on":
		a.hub.SetTrial(true, 0)
	case arg == "off":
		a.hub.SetTrial(false, 0)
	case arg != "":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > 30 {
			a.send(chat, "Aise likho: /trial on | /trial off | /trial <din 1-30>\nJaise: /trial 2")
			return true
		}
		a.hub.SetTrial(true, n)
	}
	on, days, count := a.hub.TrialInfo()
	st := "🟢 CHALU"
	if !on {
		st = "🔴 BAND"
	}
	a.send(chat, fmt.Sprintf("🎁 Free trial: %s\nKitne din ka: %d din\nAb tak liya: %d user\n\n/trial on · /trial off · /trial <din>\n(Har account aur har device ko sirf ek baar.)", st, days, count))
	return true
}
