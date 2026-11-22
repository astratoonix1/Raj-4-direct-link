package main

// Auto-index: user search kare ya request bheje aur file site par na ho, to storage channel ko
// user-session (string session) se search karke jo files mile unhe purane pipeline se index kar do.
// Telegram wala hissa (userbot.go) sirf ChanSearcher banata hai; baaki sab yahan hai (test ho sakta hai).

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	Date time.Time // message ki asli date (purani file par "naya episode" alert na jaye)
}

type ChanSearcher interface {
	Ready() bool
	// SourceMode: true ho to files alag SOURCE channel mein hain; unhe storage channel mein copy (forward) karke index karte hain.
	SourceMode() bool
	Search(ctx context.Context, q string, limit int) ([]ChanMsg, error)
	// Import: (source mode) source ke message IDs ko storage channel mein copy karo -> srcID -> storage message ID
	Import(ctx context.Context, srcIDs []int) (map[int]int, error)
	// NewSince: (source mode) afterID ke baad ke naye media messages aur channel ka current top message ID
	NewSince(ctx context.Context, afterID int) ([]ChanMsg, int, error)
	// History: channel ke offsetID se purane (0 = sabse naye se shuru) 100 messages. File wale msgs, kul messages ki ginti (0 = khatam), aur sabse chhota message ID
	History(ctx context.Context, offsetID int) (files []ChanMsg, n int, minID int, err error)
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
	bfRun  bool
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
	var fresh []ChanMsg
	for _, m := range msgs {
		if x.known(m.ID) {
			rep.Already++
			continue
		}
		if len(fresh) >= idxMaxFiles {
			break
		}
		fresh = append(fresh, m)
	}
	if err := x.indexNew(ctx, fresh, rep); err != nil && rep.Added == 0 {
		return nil, herr(502, "Files channel se la nahi paya, thodi der baad try karo.")
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

// known: ye file pehle se site par hai (ya source se pehle import ho chuki hai)?
func (x *AutoIndexer) known(id int) bool {
	if x.s.SourceMode() {
		_, ok := x.hub.SrcMapped(id) // import ho chuki: dobara nahi (admin ne hata di ho tab bhi)
		return ok
	}
	return x.cat.File(id) != nil
}

// indexNew: nayi files ko (source mode mein pehle storage channel mein copy karke) catalog mein daalo.
func (x *AutoIndexer) indexNew(ctx context.Context, fresh []ChanMsg, rep *IdxReport) error {
	if len(fresh) == 0 {
		return nil
	}
	ids := map[int]int{} // channel message id -> storage message id (FileDoc ID)
	var impErr error
	if x.s.SourceMode() {
		list := make([]int, 0, len(fresh))
		for _, m := range fresh {
			list = append(list, m.ID)
		}
		got, err := x.s.Import(ctx, list)
		impErr = err
		if len(got) > 0 {
			x.hub.SrcMapAdd(got)
		}
		ids = got
	} else {
		for _, m := range fresh {
			ids[m.ID] = m.ID
		}
	}
	for _, m := range fresh {
		fid, ok := ids[m.ID]
		if !ok {
			continue
		}
		q, rk := detectQuality(m.Name)
		created := m.Date
		if created.IsZero() {
			created = time.Now()
		}
		f := &FileDoc{ID: fid, Name: m.Name, Size: m.Size, Quality: q, QRank: rk, Created: created}
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
	return impErr
}

// SyncOnce: (source mode) source channel mein jo naye messages aaye unhe import+index karo.
// Pehli baar sirf "yahan tak dekh liya" yaad karta hai (purani poori history nahi khinchta).
func (x *AutoIndexer) SyncOnce(ctx context.Context) (*IdxReport, error) {
	if x == nil || !x.s.SourceMode() || !x.hub.IdxEnabled() || !x.s.Ready() {
		return nil, nil
	}
	last := x.hub.IdxLast()
	msgs, top, err := x.s.NewSince(ctx, last)
	if err != nil {
		return nil, err
	}
	if last == 0 {
		x.hub.SetIdxLast(top)
		return &IdxReport{Query: "sync shuru", Titles: map[string]int{}}, nil
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	rep := &IdxReport{Query: "naye messages", Titles: map[string]int{}}
	var fresh []ChanMsg
	for _, m := range msgs {
		if !isVideoName(m.Name) {
			continue
		}
		rep.Found++
		if x.known(m.ID) {
			rep.Already++
			continue
		}
		fresh = append(fresh, m)
		if len(fresh) >= 40 {
			break
		}
	}
	err = x.indexNew(ctx, fresh, rep)
	if err == nil && top > last && len(fresh) < 40 {
		x.hub.SetIdxLast(top) // 40 se zyada hon to agli baar baaki uthayenge
	} else if len(fresh) > 0 {
		x.hub.SetIdxLast(fresh[len(fresh)-1].ID)
	}
	if rep.Added > 0 && x.notify != nil {
		x.notify(rep.Summary("🔄 Auto-sync (source channel)"))
	}
	return rep, err
}

// StartSync: har `every` par SyncOnce chalao (source mode mein)
func (x *AutoIndexer) StartSync(every time.Duration) {
	if x == nil || every <= 0 {
		return
	}
	go func() {
		time.Sleep(time.Minute) // user session ready hone do
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			x.SyncOnce(ctx)
			cancel()
			<-t.C
		}
	}()
}

// ---------- Backfill: poora channel andar se scan karke sab files index karna ----------

type bfState struct {
	On      bool      `json:"on"`
	Done    bool      `json:"done"`
	Next    int       `json:"next"` // is message ID se purane messages abhi baaki (0 = sabse naye se shuru)
	Seen    int       `json:"seen"` // kitne messages dekhe
	Files   int       `json:"files"`
	Added   int       `json:"added"`
	Others  int       `json:"others"`
	Started time.Time `json:"started"`
}

func (h *Hub) BF() bfState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vip.bf
}

func (h *Hub) setBF(f func(b *bfState)) bfState {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(&h.vip.bf)
	h.put(cSettings, "bf", h.vip.bf)
	return h.vip.bf
}

// BackfillStart: shuru / resume. reset=true to bilkul shuru se (jo pehle import ho chuki wo dobara copy nahi hoti)
func (x *AutoIndexer) BackfillStart(reset bool) {
	if x == nil {
		return
	}
	x.mu.Lock()
	if x.bfRun {
		x.mu.Unlock()
		return
	}
	x.bfRun = true
	x.mu.Unlock()
	x.hub.setBF(func(b *bfState) {
		if reset || b.Done {
			*b = bfState{}
		}
		b.On = true
		if b.Started.IsZero() {
			b.Started = time.Now()
		}
	})
	go x.backfillLoop()
}

func (x *AutoIndexer) BackfillStop() { x.hub.setBF(func(b *bfState) { b.On = false }) }

// ResumeBackfill: restart ke baad agar backfill chal raha tha to wahin se aage badhao
func (x *AutoIndexer) ResumeBackfill() {
	if x != nil && x.hub.BF().On && !x.hub.BF().Done {
		x.BackfillStart(false)
	}
}

var bfPageGap = 3 * time.Second // do page ke beech ruk (Telegram ko aaram)

func (x *AutoIndexer) backfillLoop() {
	defer func() {
		x.mu.Lock()
		x.bfRun = false
		x.mu.Unlock()
	}()
	say := func(t string) {
		if x.notify != nil {
			x.notify(t)
		}
	}
	// session ready hone ka intezaar (max 5 min)
	for i := 0; i < 60 && !x.s.Ready(); i++ {
		time.Sleep(5 * time.Second)
	}
	if !x.s.Ready() {
		x.hub.setBF(func(b *bfState) { b.On = false })
		say("❌ Backfill ruk gaya: user session ready nahi hua. /idx se check karo, phir /idxall.")
		return
	}
	st := x.hub.BF()
	say(fmt.Sprintf("📦 Backfill shuru. Ab tak dekhe: %d message, nayi file: %d. Band karne ke liye /idxall stop, haal ke liye /idxall status.", st.Seen, st.Added))
	lastReport, reportedAdded := time.Now(), st.Added
	fails := 0
	for {
		st = x.hub.BF()
		if !st.On {
			say(fmt.Sprintf("⏸ Backfill ruka. Dekhe: %d message, nayi file: %d. /idxall se wahin se aage badhega.", st.Seen, st.Added))
			return
		}
		if !x.hub.IdxEnabled() {
			x.hub.setBF(func(b *bfState) { b.On = false })
			say("⏸ Backfill ruka kyunki /idxoff hai.")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		files, n, minID, err := x.s.History(ctx, st.Next)
		if err != nil {
			cancel()
			fails++
			if fails >= 6 {
				x.hub.setBF(func(b *bfState) { b.On = false })
				say("❌ Backfill ruk gaya (Telegram se baar-baar error). /idxall se dobara chalao.")
				return
			}
			time.Sleep(time.Duration(fails) * 30 * time.Second)
			continue
		}
		fails = 0
		if n == 0 { // history khatam
			cancel()
			fin := x.hub.setBF(func(b *bfState) { b.On, b.Done = false, true })
			say(fmt.Sprintf("✅ Backfill POORA. %d message dekhe, %d nayi file site par aayi.\n⚠️ %d file ka TMDB match nahi mila (“Other” mein gayi), /others se dekho aur /fix karo.", fin.Seen, fin.Added, fin.Others))
			return
		}
		sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
		var fresh []ChanMsg
		for _, m := range files {
			if isVideoName(m.Name) && !x.known(m.ID) {
				fresh = append(fresh, m)
			}
		}
		rep := &IdxReport{Titles: map[string]int{}}
		for i := 0; i < len(fresh); i += 40 {
			if err := x.indexNew(ctx, fresh[i:min(i+40, len(fresh))], rep); err != nil {
				break
			}
		}
		cancel()
		cur := x.hub.setBF(func(b *bfState) {
			b.Next = minID
			b.Seen += n
			b.Files += len(files)
			b.Added += rep.Added
			b.Others += rep.Others
		})
		if time.Since(lastReport) > 20*time.Minute || cur.Added-reportedAdded >= 1000 {
			say(fmt.Sprintf("📦 Backfill chal raha hai: %d message dekhe, %d nayi file index hui, %d “Other” mein.", cur.Seen, cur.Added, cur.Others))
			lastReport, reportedAdded = time.Now(), cur.Added
		}
		time.Sleep(bfPageGap)
	}
}

func (x *AutoIndexer) BackfillStatus() string {
	b := x.hub.BF()
	state := "⏹ ruka hua"
	switch {
	case b.Done:
		state = "✅ poora ho chuka"
	case b.On:
		state = "▶️ chal raha hai"
	}
	return fmt.Sprintf("📦 BACKFILL: %s\nDekhe: %d message (%d file wale)\nNayi file site par: %d\n“Other” mein: %d\nAgla pointer: %d", state, b.Seen, b.Files, b.Added, b.Others, b.Next)
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

// ---------- settings (on/off), source map, sync pointer ----------

func (h *Hub) SrcMapped(srcID int) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.vip.srcMap[srcID]
	return v, ok
}

// SrcMapAdd: source message -> storage message (hamesha ke liye yaad)
func (h *Hub) SrcMapAdd(m map[int]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.vip.srcMap == nil {
		h.vip.srcMap = map[int]int{}
	}
	for k, v := range m {
		h.vip.srcMap[k] = v
	}
	doc := make(map[string]int, len(h.vip.srcMap))
	for k, v := range h.vip.srcMap {
		doc[strconv.Itoa(k)] = v
	}
	h.put(cSettings, "srcmap", map[string]any{"m": doc})
}

func (h *Hub) IdxLast() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vip.idxLast
}

func (h *Hub) SetIdxLast(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vip.idxLast = n
	h.put(cSettings, "idxsync", map[string]int{"last": n})
}

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
		mode := "Mode: storage channel mein seedha search"
		if a.idx != nil && a.idx.s.SourceMode() {
			mode = fmt.Sprintf("Mode: SOURCE channel → storage channel mein copy. Yaad: %d file, sync pointer: %d", len(a.hub.srcMapCopy()), a.hub.IdxLast())
		}
		a.send(chat, "🔎 AUTO-INDEX: "+on+"\n"+sess+"\n"+mode+"\n\nJab user search/request kare aur file site par na ho, to storage channel mein dhundh kar index ho jaati hai.\n\n/idxon — chalu\n/idxoff — band\n/idxscan <naam> — abhi channel mein dhundho (jaise /idxscan raj)\n/idxsync — source channel ke naye messages abhi khincho\n/idxall — poora channel andar se scan karke sab files index karo (status/stop/reset)")
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
	case "idxall":
		if a.idx == nil {
			a.send(chat, "❌ TG_STRING_SESSION set nahi hai.")
			return true
		}
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "stop":
			a.idx.BackfillStop()
			a.send(chat, "⏸ Backfill rok raha hun (current page poora hone ke baad ruk jayega).")
		case "status":
			a.send(chat, a.idx.BackfillStatus())
		case "reset":
			a.idx.BackfillStart(true)
			a.send(chat, "🔄 Backfill bilkul shuru se chala (jo files pehle copy ho chuki wo dobara copy nahi hongi).")
		default:
			a.idx.BackfillStart(false)
			a.send(chat, "📦 Backfill chalu. Poora channel andar se scan hoga aur har file index hogi. Bahut files hon to ghante lag sakte hain, ruk-ruk kar report aati rahegi.\n\n/idxall status — haal\n/idxall stop — roko (baad mein /idxall se wahin se aage)\n/idxall reset — shuru se")
		}
	case "idxsync":
		if a.idx == nil || !a.idx.s.SourceMode() {
			a.send(chat, "❌ SOURCE_CHANNEL_ID set nahi hai (sync sirf source channel mode mein hota hai).")
			return true
		}
		a.send(chat, "🔄 Source channel ke naye messages dekh raha hun…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			rep, err := a.idx.SyncOnce(ctx)
			switch {
			case err != nil:
				a.send(chat, "❌ "+err.Error())
			case rep == nil:
				a.send(chat, "ℹ️ Sync abhi nahi ho sakta (auto-index band ya session ready nahi).")
			case rep.Added == 0:
				a.send(chat, "ℹ️ Koi nayi file nahi mili.")
			}
		}()
	default:
		return false
	}
	return true
}

func (h *Hub) srcMapCopy() map[int]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[int]int, len(h.vip.srcMap))
	for k, v := range h.vip.srcMap {
		out[k] = v
	}
	return out
}
