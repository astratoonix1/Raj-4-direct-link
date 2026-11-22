package main

// Auto-index: user search kare ya request bheje aur file site par na ho, to storage channel ko
// user-session (string session) se search karke jo files mile unhe purane pipeline se index kar do.
// Telegram wala hissa (userbot.go) sirf ChanSearcher banata hai; baaki sab yahan hai (test ho sakta hai).

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ChanMsg: channel ka ek message jisme file hai (ID = channel message id = FileDoc ID)
type ChanMsg struct {
	ID   int
	Name string
	Size int64
}

type ChanSearcher interface {
	Ready() bool
	Search(ctx context.Context, q string, limit int) ([]ChanMsg, error)
}

type IdxReport struct {
	Query   string         `json:"query"`
	Found   int            `json:"found"`   // channel mein relevant files
	Already int            `json:"already"` // pehle se site par
	Added   int            `json:"added"`   // abhi nayi index hui
	Others  int            `json:"others"`  // TMDB match nahi mila ("Other" mein gayi)
	Titles  map[string]int `json:"titles"`  // title -> nayi files
	Cached  bool           `json:"cached,omitempty"`
}

const (
	idxCacheTTL = 30 * time.Minute
	idxMaxFiles = 60 // ek search mein itni se zyada nayi files index nahi
	idxMinChars = 3
)

type idxCached struct {
	rep *IdxReport
	at  time.Time
}

type AutoIndexer struct {
	cat    *Catalog
	pipe   *Pipeline
	hub    *Hub
	s      ChanSearcher
	notify func(text string) // admin ko summary

	mu     sync.Mutex
	cache  map[string]idxCached
	flight map[string]chan struct{}
}

func NewAutoIndexer(cat *Catalog, pipe *Pipeline, hub *Hub, s ChanSearcher, notify func(string)) *AutoIndexer {
	return &AutoIndexer{cat: cat, pipe: pipe, hub: hub, s: s, notify: notify, cache: map[string]idxCached{}, flight: map[string]chan struct{}{}}
}

var videoExts = map[string]bool{"mkv": true, "mp4": true, "avi": true, "mov": true, "webm": true, "m4v": true, "ts": true,
	"flv": true, "wmv": true, "mpg": true, "mpeg": true, "3gp": true, "m2ts": true}

func isVideoName(name string) bool {
	i := strings.LastIndex(name, ".")
	return i >= 0 && videoExts[strings.ToLower(name[i+1:])]
}

// queryTokens: "Raj 2019" -> ["raj","2019"]
func queryTokens(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127) }) {
		if len([]rune(w)) >= 1 {
			out = append(out, w)
		}
	}
	return out
}

func idxKey(q string) string { return strings.Join(queryTokens(q), " ") }

// relevant: file ke naam mein query ka har shabd poora shabd ho (ya 4+ akshar ka shuruaati hissa).
// "raj" -> "Raj.2019.mkv" haan, "Rajesh.mkv" nahi. "spiderman" -> "Spider.Man.mkv" bhi haan.
func relevant(name string, toks []string) bool {
	if !isVideoName(name) || len(toks) == 0 {
		return false
	}
	words := queryTokens(name[:strings.LastIndex(name, ".")])
	all := true
	for _, t := range toks {
		ok := false
		for _, w := range words {
			if w == t || (len([]rune(t)) >= 4 && strings.HasPrefix(w, t)) {
				ok = true
				break
			}
		}
		if !ok {
			all = false
			break
		}
	}
	if all {
		return true
	}
	// shabd alag-alag likhe hon ("Spider Man" vs "spiderman")
	nq := normText(strings.Join(toks, ""))
	return len(nq) >= 5 && strings.Contains(normText(name), nq)
}

func queryVariants(q string) []string {
	q = strings.TrimSpace(q)
	v := []string{q}
	if strings.Contains(q, " ") {
		v = append(v, strings.ReplaceAll(q, " ", "."), strings.ReplaceAll(q, " ", "_"))
	}
	return v
}

func (x *AutoIndexer) Enabled() bool { return x != nil && x.hub.IdxEnabled() }
func (x *AutoIndexer) Ready() bool   { return x != nil && x.s != nil && x.s.Ready() }

// Discover: channel mein query search karke nayi files index karo.
func (x *AutoIndexer) Discover(ctx context.Context, query string) (*IdxReport, error) {
	if x == nil || !x.hub.IdxEnabled() {
		return nil, herr(503, "Auto-index band hai.")
	}
	key := idxKey(query)
	if len([]rune(strings.ReplaceAll(key, " ", ""))) < idxMinChars {
		return nil, herr(400, "Naam thoda lamba likho.")
	}
	toks := queryTokens(query)

	x.mu.Lock()
	if c, ok := x.cache[key]; ok && time.Since(c.at) < idxCacheTTL {
		r := *c.rep
		r.Cached, r.Added = true, 0 // cache se aaye to "nayi" file nahi
		x.mu.Unlock()
		return &r, nil
	}
	if ch, ok := x.flight[key]; ok { // wahi search chal rahi hai: uska intezaar
		x.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		x.mu.Lock()
		c := x.cache[key]
		x.mu.Unlock()
		if c.rep == nil {
			return nil, herr(502, "Search poori nahi hui, dobara try karo.")
		}
		r := *c.rep
		r.Cached, r.Added = true, 0
		return &r, nil
	}
	ch := make(chan struct{})
	x.flight[key] = ch
	x.mu.Unlock()
	defer func() {
		x.mu.Lock()
		delete(x.flight, key)
		x.mu.Unlock()
		close(ch)
	}()

	if !x.s.Ready() {
		return nil, herr(503, "Channel search abhi ready nahi hai.")
	}
	if !x.hub.rl.allow("idx:global", 12, time.Minute) {
		return nil, herr(429, "Abhi bahut search ho rahi hain, thodi der baad try karo.")
	}

	seen := map[int]bool{}
	var msgs []ChanMsg
	for _, v := range queryVariants(query) {
		res, err := x.s.Search(ctx, v, 100)
		if err != nil {
			if len(msgs) == 0 {
				return nil, herr(502, "Channel search nahi ho paya.")
			}
			break
		}
		for _, m := range res {
			if !seen[m.ID] && relevant(m.Name, toks) {
				seen[m.ID] = true
				msgs = append(msgs, m)
			}
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID }) // purani pehle: episode kram mein

	rep := &IdxReport{Query: strings.TrimSpace(query), Found: len(msgs), Titles: map[string]int{}}
	for _, m := range msgs {
		if x.cat.File(m.ID) != nil {
			rep.Already++
			continue
		}
		if rep.Added >= idxMaxFiles {
			break
		}
		q, rk := detectQuality(m.Name)
		f := &FileDoc{ID: m.ID, Name: m.Name, Size: m.Size, Quality: q, QRank: rk, Created: time.Now()}
		if err := x.cat.AddFile(ctx, f); err != nil {
			continue
		}
		res, err := x.pipe.Process(ctx, f.ID)
		rep.Added++
		if err != nil {
			rep.Others++
			rep.Titles[m.Name]++
			continue
		}
		rep.Titles[res.Title]++
		if !res.Matched {
			rep.Others++
		}
	}
	x.mu.Lock()
	x.cache[key] = idxCached{rep: rep, at: time.Now()}
	if len(x.cache) > 500 {
		x.cache = map[string]idxCached{key: x.cache[key]}
	}
	x.mu.Unlock()
	if rep.Added > 0 && x.notify != nil {
		x.notify(rep.Summary("🔎 Auto-index"))
	}
	out := *rep
	return &out, nil
}

// Summary: admin ke liye chhota report
func (r *IdxReport) Summary(head string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — “%s”\n✅ %d nayi file index hui", head, r.Query, r.Added)
	if r.Already > 0 {
		fmt.Fprintf(&b, " (pehle se %d thi)", r.Already)
	}
	names := make([]string, 0, len(r.Titles))
	for t := range r.Titles {
		names = append(names, t)
	}
	sort.Strings(names)
	for i, t := range names {
		if i == 12 {
			fmt.Fprintf(&b, "\n…aur %d title", len(names)-12)
			break
		}
		fmt.Fprintf(&b, "\n• %s (%d)", t, r.Titles[t])
	}
	if r.Others > 0 {
		fmt.Fprintf(&b, "\n⚠️ %d file ka TMDB match nahi mila, “Other” mein gayi. Theek karna ho to /fix use karo.", r.Others)
	}
	return b.String()
}

// OnRequest: user ne request bheji to channel mein bhi dhundo; mil gayi to FulfillRequests khud notice bhej dega.
func (x *AutoIndexer) OnRequest(r *Request) {
	if x == nil || r == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		x.Discover(ctx, r.Name)
	}()
}

// ---------- settings (on/off) ----------

func (h *Hub) IdxEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.vip.idxOff
}

func (h *Hub) SetIdxEnabled(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vip.idxOff = !on
	h.put(cSettings, "idx", map[string]bool{"off": !on})
}

// ---------- site API: POST /api/discover {q} ----------

func (a *Accounts) discoverHandler(mux *http.ServeMux) {
	mux.HandleFunc("/api/discover", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Q string `json:"q"`
		}
		if !post(w, r, 1<<10, &b) {
			return
		}
		if a.Idx == nil || !a.Idx.Enabled() {
			writeJSON(w, map[string]any{"enabled": false, "added": 0})
			return
		}
		who, _ := a.who(r)
		if !a.hub.rl.allow("disc:"+who, 8, time.Hour) || !a.hub.rl.allow("discip:"+clientIP(r), 20, time.Hour) {
			fail(w, herr(429, "Bahut zyada search. Thodi der baad try karo."))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		rep, err := a.Idx.Discover(ctx, b.Q)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]any{"enabled": true, "added": rep.Added, "found": rep.Found, "already": rep.Already})
	})
}

// ---------- bot commands: /idx /idxon /idxoff /idxscan <naam> ----------

func (a *App) idxCommand(m *tgbotapi.Message, arg string) bool {
	chat := m.Chat.ID
	switch m.Command() {
	case "idx", "idxon", "idxoff":
		switch m.Command() {
		case "idxon":
			a.hub.SetIdxEnabled(true)
		case "idxoff":
			a.hub.SetIdxEnabled(false)
		}
		on := "🔴 OFF"
		if a.hub.IdxEnabled() {
			on = "🟢 ON"
		}
		sess := "❌ TG_STRING_SESSION nahi laga (auto-index kaam nahi karega)"
		if a.idx != nil {
			sess = "❌ Session connect nahi hua (logs dekho)"
			if a.idx.Ready() {
				sess = "✅ User session connected"
			}
		}
		a.send(chat, "🔎 AUTO-INDEX: "+on+"\n"+sess+"\n\nJab user search/request kare aur file site par na ho, to storage channel mein dhundh kar index ho jaati hai.\n\n/idxon — chalu\n/idxoff — band\n/idxscan <naam> — abhi channel mein dhundho (jaise /idxscan raj)")
	case "idxscan":
		if a.idx == nil {
			a.send(chat, "❌ TG_STRING_SESSION set nahi hai.")
			return true
		}
		if strings.TrimSpace(arg) == "" {
			a.send(chat, "Aise likho: /idxscan <naam>\nJaise: /idxscan raj")
			return true
		}
		a.send(chat, "🔎 Channel mein dhundh raha hun…")
		go func(q string) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			// admin ke liye cache chhod kar fresh scan
			a.idx.mu.Lock()
			delete(a.idx.cache, idxKey(q))
			a.idx.mu.Unlock()
			rep, err := a.idx.Discover(ctx, q)
			switch {
			case err != nil:
				a.send(chat, "❌ "+err.Error())
			case rep.Found == 0:
				a.send(chat, "😕 Channel mein “"+q+"” naam ki koi video file nahi mili.")
			case rep.Added == 0:
				a.send(chat, fmt.Sprintf("ℹ️ %d file mili, sab pehle se site par hain.", rep.Found))
			default:
				a.send(chat, rep.Summary("✅ Scan poora"))
			}
		}(strings.TrimSpace(arg))
	default:
		return false
	}
	return true
}
