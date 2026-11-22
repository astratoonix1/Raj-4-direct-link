package main

// Naye episode ka alert: jis user ne title My List mein rakha hai, use bell mein notice milta hai.
// Ek saath kai episode upload ho to ek hi notice (epDelay tak jama karke).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Note: user ke naam ka personal notice (bell icon mein)
type Note struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	Key  string    `json:"key,omitempty"` // title key: click karke seedha title khule
	T    time.Time `json:"t"`
}

const (
	maxNotes    = 20
	noteMaxAge  = 30 * 24 * time.Hour
	freshWindow = 6 * time.Hour // file itni nayi ho tabhi alert
)

var epDelay = 10 * time.Minute

type epBatch struct {
	title string
	eps   map[[2]int]bool
}

type epState struct {
	mu    sync.Mutex
	batch map[string]*epBatch
}

// NoteAttach: Catalog.OnAttach se judta hai. Sirf episode wali nayi file par chalta hai.
func (h *Hub) NoteAttach(key, title string, season, episode int, fileAge time.Duration) {
	if key == "" || (season == 0 && episode == 0) || fileAge > freshWindow {
		return
	}
	h.ep.mu.Lock()
	defer h.ep.mu.Unlock()
	if h.ep.batch == nil {
		h.ep.batch = map[string]*epBatch{}
	}
	b := h.ep.batch[key]
	if b == nil {
		b = &epBatch{title: title, eps: map[[2]int]bool{}}
		h.ep.batch[key] = b
		time.AfterFunc(epDelay, func() { h.flushEp(key) })
	}
	b.eps[[2]int{season, episode}] = true
}

func epLabel(s, e int) string {
	switch {
	case s > 0 && e > 0:
		return fmt.Sprintf("S%02dE%02d", s, e)
	case s > 0:
		return fmt.Sprintf("Season %d", s)
	}
	return fmt.Sprintf("Episode %d", e)
}

func (h *Hub) flushEp(key string) {
	h.ep.mu.Lock()
	b := h.ep.batch[key]
	delete(h.ep.batch, key)
	h.ep.mu.Unlock()
	if b == nil || len(b.eps) == 0 {
		return
	}
	var list [][2]int
	for k := range b.eps {
		list = append(list, k)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i][0] != list[j][0] {
			return list[i][0] < list[j][0]
		}
		return list[i][1] < list[j][1]
	})
	var text string
	if len(list) == 1 {
		text = fmt.Sprintf("🆕 %s ka naya episode aa gaya (%s). Dekhne ke liye tap karo.", b.title, epLabel(list[0][0], list[0][1]))
	} else {
		text = fmt.Sprintf("🆕 %s ke %d naye episode aa gaye (%s se %s). Dekhne ke liye tap karo.", b.title, len(list), epLabel(list[0][0], list[0][1]), epLabel(list[len(list)-1][0], list[len(list)-1][1]))
	}
	h.notifyListUsers(key, text)
}

// notifyListUsers: jin users ki My List mein key hai unhe Note do. Kitno ko gaya wo wapas.
func (h *Hub) notifyListUsers(key, text string) int {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, u := range h.users {
		if u.Blocked || len(u.List) == 0 || !strings.Contains(string(u.List), key) {
			continue
		}
		var l []struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(u.List, &l) != nil {
			continue
		}
		has := false
		for _, x := range l {
			if x.Key == key {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		u.Notes = append(pruneNotes(u.Notes, now), Note{ID: randHex(4), Text: text, Key: key, T: now})
		if len(u.Notes) > maxNotes {
			u.Notes = u.Notes[len(u.Notes)-maxNotes:]
		}
		h.put(cUsers, u.ID, u)
		n++
	}
	return n
}

func pruneNotes(ns []Note, now time.Time) []Note {
	out := ns[:0:0]
	for _, x := range ns {
		if now.Sub(x.T) < noteMaxAge {
			out = append(out, x)
		}
	}
	return out
}

// NotesOf: user ke notice (naye pehle nahi, purane pehle — client sort karta hai)
func (h *Hub) NotesOf(uid string) []Note {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return []Note{}
	}
	out := pruneNotes(u.Notes, time.Now())
	if out == nil {
		out = []Note{}
	}
	return out
}

// ClearNotes: id khaali ho to sab hatao
func (h *Hub) ClearNotes(uid, id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return 0
	}
	n := 0
	var keep []Note
	for _, x := range u.Notes {
		if id == "" || x.ID == id {
			n++
			continue
		}
		keep = append(keep, x)
	}
	if n > 0 {
		u.Notes = keep
		h.put(cUsers, u.ID, u)
	}
	return n
}
