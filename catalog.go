package main

// catalog.go — titles + files ki list (memory mein), aur site ke liye JSON views.
// Persistence ek Store interface se hoti hai (MongoDB), is file mein sirf std library.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Cast struct {
	Name  string `bson:"name" json:"name"`
	Role  string `bson:"role,omitempty" json:"role,omitempty"`
	Photo string `bson:"photo,omitempty" json:"photo,omitempty"`
}

// Part = ek film ke series ka hissa (Part 2, Part 3 ...), TMDB collection se.
type Part struct {
	TMDBID int    `bson:"tmdb_id" json:"tmdb_id"`
	Title  string `bson:"title" json:"title"`
	Year   int    `bson:"year,omitempty" json:"year,omitempty"`
	Poster string `bson:"poster,omitempty" json:"poster,omitempty"`
}

type TitleDoc struct {
	Key            string    `bson:"_id" json:"key"` // movie:603 | tv:1396 | other:<naam>
	Kind           string    `bson:"kind" json:"kind"`
	Category       string    `bson:"category" json:"category"` // Movie|Series|Anime|Cartoon|Other
	Title          string    `bson:"title" json:"title"`
	Overview       string    `bson:"overview,omitempty" json:"overview,omitempty"`
	Tagline        string    `bson:"tagline,omitempty" json:"tagline,omitempty"`
	Year           int       `bson:"year,omitempty" json:"year,omitempty"`
	Rating         float64   `bson:"rating,omitempty" json:"rating,omitempty"`
	Runtime        int       `bson:"runtime,omitempty" json:"runtime,omitempty"`
	Seasons        int       `bson:"seasons,omitempty" json:"seasons,omitempty"`
	Genres         []string  `bson:"genres,omitempty" json:"genres,omitempty"`
	Poster         string    `bson:"poster,omitempty" json:"poster,omitempty"`
	Backdrop       string    `bson:"backdrop,omitempty" json:"backdrop,omitempty"`
	Cast           []Cast    `bson:"cast,omitempty" json:"cast,omitempty"`
	Trailer        string    `bson:"trailer,omitempty" json:"trailer,omitempty"`
	TMDBID         int       `bson:"tmdb_id,omitempty" json:"-"`
	CollectionID   int       `bson:"collection_id,omitempty" json:"-"`
	CollectionName string    `bson:"collection_name,omitempty" json:"collection_name,omitempty"`
	Parts          []Part    `bson:"parts,omitempty" json:"-"`
	Aliases        []string  `bson:"aliases,omitempty" json:"-"`
	Views          int       `bson:"views" json:"-"`
	Locked         bool      `bson:"locked,omitempty" json:"-"`  // admin ne khud set kiya (/fix, /cat)
	Access         string    `bson:"access,omitempty" json:"-"`  // "" = auto (saal ka rule) | "premium" | "free"
	Premium        bool      `bson:"-" json:"premium,omitempty"` // sirf dikhane ke liye (calculate hota hai)
	Created        time.Time `bson:"created" json:"-"`
	Updated        time.Time `bson:"updated" json:"-"`
}

type FileDoc struct {
	ID       int       `bson:"_id" json:"id"` // storage channel ka message id
	Name     string    `bson:"name" json:"name"`
	Size     int64     `bson:"size" json:"size"`
	TitleKey string    `bson:"title_key" json:"-"`
	Season   int       `bson:"season,omitempty" json:"season,omitempty"`
	Episode  int       `bson:"episode,omitempty" json:"episode,omitempty"`
	Part     int       `bson:"part,omitempty" json:"part,omitempty"`
	Quality  string    `bson:"quality" json:"quality"`
	QRank    int       `bson:"qrank" json:"-"`
	Created  time.Time `bson:"created" json:"-"`
}

type Store interface {
	LoadAll(ctx context.Context) ([]*TitleDoc, []*FileDoc, error)
	SaveTitle(ctx context.Context, t *TitleDoc) error
	SaveFile(ctx context.Context, f *FileDoc) error
	DeleteFile(ctx context.Context, id int) error
	DeleteTitle(ctx context.Context, key string) error
	AddViews(ctx context.Context, key string, n int) error
}

type Catalog struct {
	mu       sync.RWMutex
	store    Store
	titles   map[string]*TitleDoc
	files    map[int]*FileDoc
	byTitle  map[string][]int  // title key -> file ids
	aliases  map[string]string // alias -> title key
	premFrom int               // is saal se premium (0 = off)
}

func NewCatalog(ctx context.Context, store Store) (*Catalog, error) {
	c := &Catalog{store: store, titles: map[string]*TitleDoc{}, files: map[int]*FileDoc{},
		byTitle: map[string][]int{}, aliases: map[string]string{}}
	ts, fs, err := store.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		t.Title = strings.ToValidUTF8(t.Title, "") // purane kharab naam saaf
		c.titles[t.Key] = t
		for _, a := range t.Aliases {
			c.aliases[a] = t.Key
		}
	}
	for _, f := range fs {
		c.files[f.ID] = f
		c.byTitle[f.TitleKey] = append(c.byTitle[f.TitleKey], f.ID)
	}
	return c, nil
}

// ---------- likhna ----------

func (c *Catalog) AddFile(ctx context.Context, f *FileDoc) error {
	c.mu.Lock()
	if old, ok := c.files[f.ID]; ok {
		c.unlink(old)
	}
	c.files[f.ID] = f
	if f.TitleKey != "" {
		c.byTitle[f.TitleKey] = append(c.byTitle[f.TitleKey], f.ID)
	}
	c.mu.Unlock()
	return c.store.SaveFile(ctx, f)
}

func (c *Catalog) unlink(f *FileDoc) { // lock pehle se liya hona chahiye
	ids := c.byTitle[f.TitleKey]
	for i, id := range ids {
		if id == f.ID {
			c.byTitle[f.TitleKey] = append(ids[:i:i], ids[i+1:]...)
			break
		}
	}
	if len(c.byTitle[f.TitleKey]) == 0 {
		delete(c.byTitle, f.TitleKey)
	}
}

// Attach file ko title se jodta hai (season/episode ke saath).
func (c *Catalog) Attach(ctx context.Context, fileID int, titleKey string, season, episode, part int) error {
	c.mu.Lock()
	f := c.files[fileID]
	if f == nil {
		c.mu.Unlock()
		return fmt.Errorf("file %d catalog mein nahi hai", fileID)
	}
	oldKey := f.TitleKey
	c.unlink(f)
	dropOld := false
	if oldKey != "" && oldKey != titleKey && len(c.byTitle[oldKey]) == 0 {
		if ot := c.titles[oldKey]; ot != nil {
			for _, a := range ot.Aliases {
				if c.aliases[a] == oldKey {
					delete(c.aliases, a)
				}
			}
			delete(c.titles, oldKey)
			dropOld = true
		}
	}
	f.TitleKey, f.Season, f.Episode, f.Part = titleKey, season, episode, part
	c.byTitle[titleKey] = append(c.byTitle[titleKey], f.ID)
	if t := c.titles[titleKey]; t != nil {
		t.Updated = time.Now()
	}
	cp := *f
	var tcopy *TitleDoc
	if t := c.titles[titleKey]; t != nil {
		x := *t
		tcopy = &x
	}
	c.mu.Unlock()
	if dropOld {
		_ = c.store.DeleteTitle(ctx, oldKey)
	}
	if tcopy != nil {
		_ = c.store.SaveTitle(ctx, tcopy)
	}
	return c.store.SaveFile(ctx, &cp)
}

func (c *Catalog) UpsertTitle(ctx context.Context, t *TitleDoc) error {
	c.mu.Lock()
	if old := c.titles[t.Key]; old != nil {
		t.Views = old.Views
		t.Created = old.Created
		t.Aliases = mergeStrings(old.Aliases, t.Aliases)
		t.Locked = t.Locked || old.Locked
	}
	now := time.Now()
	if t.Created.IsZero() {
		t.Created = now
	}
	t.Updated = now
	c.titles[t.Key] = t
	for _, a := range t.Aliases {
		c.aliases[a] = t.Key
	}
	cp := *t
	c.mu.Unlock()
	return c.store.SaveTitle(ctx, &cp)
}

func (c *Catalog) AddAlias(ctx context.Context, key, alias string) {
	if alias == "" {
		return
	}
	c.mu.Lock()
	t := c.titles[key]
	if t == nil {
		c.mu.Unlock()
		return
	}
	t.Aliases = mergeStrings(t.Aliases, []string{alias})
	c.aliases[alias] = key
	cp := *t
	c.mu.Unlock()
	_ = c.store.SaveTitle(ctx, &cp)
}

func (c *Catalog) SetCategory(ctx context.Context, key, cat string) bool {
	c.mu.Lock()
	t := c.titles[key]
	if t == nil {
		c.mu.Unlock()
		return false
	}
	t.Category, t.Locked, t.Updated = cat, true, time.Now()
	cp := *t
	c.mu.Unlock()
	_ = c.store.SaveTitle(ctx, &cp)
	return true
}

func (c *Catalog) DeleteFile(ctx context.Context, id int) bool {
	c.mu.Lock()
	f := c.files[id]
	if f == nil {
		c.mu.Unlock()
		return false
	}
	c.unlink(f)
	delete(c.files, id)
	key := f.TitleKey
	var dropTitle bool
	if len(c.byTitle[key]) == 0 {
		if t := c.titles[key]; t != nil {
			for _, a := range t.Aliases {
				delete(c.aliases, a)
			}
			delete(c.titles, key)
			dropTitle = true
		}
	}
	c.mu.Unlock()
	_ = c.store.DeleteFile(ctx, id)
	if dropTitle {
		_ = c.store.DeleteTitle(ctx, key)
	}
	return true
}

func (c *Catalog) AddView(fileID int) {
	c.mu.Lock()
	f := c.files[fileID]
	var key string
	if f != nil {
		if t := c.titles[f.TitleKey]; t != nil {
			t.Views++
			key = t.Key
		}
	}
	c.mu.Unlock()
	if key != "" {
		go func() { _ = c.store.AddViews(context.Background(), key, 1) }()
	}
}

// ---------- padhna ----------

func (c *Catalog) FindAlias(alias string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.aliases[alias]
	if ok && c.titles[k] == nil {
		return "", false
	}
	return k, ok
}

func (c *Catalog) Title(key string) *TitleDoc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if t := c.titles[key]; t != nil {
		cp := *t
		return &cp
	}
	return nil
}

func (c *Catalog) File(id int) *FileDoc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if f := c.files[id]; f != nil {
		cp := *f
		return &cp
	}
	return nil
}

// Others = jin titles ka TMDB match nahi mila (retry ke liye).
func (c *Catalog) OtherFileIDs() []int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []int
	for _, f := range c.files {
		if t := c.titles[f.TitleKey]; t != nil && t.Kind == "other" && !t.Locked {
			out = append(out, f.ID)
		}
	}
	sort.Ints(out)
	return out
}

func (c *Catalog) FilesOfTitle(key string) []int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]int(nil), c.byTitle[key]...)
}

func (c *Catalog) Stats() (titles, files int, byCat map[string]int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	byCat = map[string]int{}
	for _, t := range c.titles {
		byCat[t.Category]++
	}
	return len(c.titles), len(c.files), byCat
}

// ---------- site ke liye views ----------

type Card struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Year     int      `json:"year,omitempty"`
	Rating   float64  `json:"rating,omitempty"`
	Poster   string   `json:"poster,omitempty"`
	Backdrop string   `json:"backdrop,omitempty"`
	Category string   `json:"category"`
	Kind     string   `json:"kind"`
	Genres   []string `json:"genres,omitempty"`
	Overview string   `json:"overview,omitempty"`
	Files    int      `json:"files"`
	Premium  bool     `json:"premium,omitempty"`
}

func (c *Catalog) card(t *TitleDoc, withOverview bool) Card {
	cd := Card{Key: t.Key, Title: t.Title, Year: t.Year, Rating: t.Rating, Poster: t.Poster,
		Backdrop: t.Backdrop, Category: t.Category, Kind: t.Kind, Genres: t.Genres, Files: len(c.byTitle[t.Key]), Premium: c.premium(t)}
	if withOverview {
		o := t.Overview
		if r := []rune(o); len(r) > 260 {
			o = string(r[:257]) + "..."
		}
		cd.Overview = o
	}
	return cd
}

// visible titles (kam se kam ek file wale), naye pehle. Sorting Go mein hoti hai.
func (c *Catalog) visibleSorted() []*TitleDoc {
	out := make([]*TitleDoc, 0, len(c.titles))
	for k, t := range c.titles {
		if len(c.byTitle[k]) > 0 {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

type Row struct {
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Items []Card `json:"items"`
}

type HomeView struct {
	Heroes []Card `json:"heroes"`
	Rows   []Row  `json:"rows"`
	Total  int    `json:"total"`
}

var homeGenres = []string{"Action", "Comedy", "Horror", "Drama", "Science Fiction", "Thriller", "Romance", "Family"}

func (c *Catalog) Home() HomeView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	all := c.visibleSorted()
	hv := HomeView{Total: len(all), Rows: []Row{}, Heroes: []Card{}}
	for _, t := range all {
		if t.Backdrop != "" && len(hv.Heroes) < 5 && t.Category != "Other" {
			hv.Heroes = append(hv.Heroes, c.card(t, true))
		}
	}
	const maxRow = 30
	add := func(name, slug string, items []*TitleDoc) {
		if len(items) == 0 {
			return
		}
		if len(items) > maxRow {
			items = items[:maxRow]
		}
		r := Row{Name: name, Slug: slug}
		for _, t := range items {
			r.Items = append(r.Items, c.card(t, false))
		}
		hv.Rows = append(hv.Rows, r)
	}
	add("New Releases", "new", all)

	top := append([]*TitleDoc(nil), all...)
	sort.SliceStable(top, func(i, j int) bool { return top[i].Views > top[j].Views })
	var top10 []*TitleDoc
	for _, t := range top {
		if t.Views > 0 && len(top10) < 10 {
			top10 = append(top10, t)
		}
	}
	add("Top 10 on AstraToonix", "top", top10)

	byCat := func(cat string) []*TitleDoc {
		var o []*TitleDoc
		for _, t := range all {
			if t.Category == cat {
				o = append(o, t)
			}
		}
		return o
	}
	add("Movies", "cat:Movie", byCat("Movie"))
	add("TV Shows", "cat:Series", byCat("Series"))
	add("Anime", "cat:Anime", byCat("Anime"))
	add("Cartoon", "cat:Cartoon", byCat("Cartoon"))
	for _, g := range homeGenres {
		var o []*TitleDoc
		for _, t := range all {
			if hasStr(t.Genres, g) {
				o = append(o, t)
			}
		}
		add(g, "genre:"+g, o)
	}
	add("Other", "cat:Other", byCat("Other"))
	return hv
}

type BrowseView struct {
	Items   []Card   `json:"items"`
	Total   int      `json:"total"`
	HasMore bool     `json:"has_more"`
	Genres  []string `json:"genres"`
}

func (c *Catalog) Browse(cat, genre, sortBy string, page int) BrowseView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	all := c.visibleSorted()
	var list []*TitleDoc
	gset := map[string]int{}
	for _, t := range all {
		if cat != "" && cat != "all" && !strings.EqualFold(t.Category, cat) {
			continue
		}
		for _, g := range t.Genres {
			gset[g]++
		}
		if genre != "" && !hasStr(t.Genres, genre) {
			continue
		}
		list = append(list, t)
	}
	switch sortBy {
	case "rating":
		sort.SliceStable(list, func(i, j int) bool { return list[i].Rating > list[j].Rating })
	case "az":
		sort.SliceStable(list, func(i, j int) bool {
			return strings.ToLower(list[i].Title) < strings.ToLower(list[j].Title)
		})
	case "popular":
		sort.SliceStable(list, func(i, j int) bool { return list[i].Views > list[j].Views })
	}
	const per = 36
	if page < 0 {
		page = 0
	}
	total := len(list)
	start := page * per
	if start > total {
		start = total
	}
	end := start + per
	if end > total {
		end = total
	}
	bv := BrowseView{Total: total, HasMore: end < total, Items: []Card{}, Genres: []string{}}
	for _, t := range list[start:end] {
		bv.Items = append(bv.Items, c.card(t, false))
	}
	type gc struct {
		n string
		c int
	}
	var gl []gc
	for g, n := range gset {
		gl = append(gl, gc{g, n})
	}
	sort.Slice(gl, func(i, j int) bool {
		if gl[i].c != gl[j].c {
			return gl[i].c > gl[j].c
		}
		return gl[i].n < gl[j].n
	})
	for _, g := range gl {
		bv.Genres = append(bv.Genres, g.n)
	}
	return bv
}

func (c *Catalog) Search(q string) []Card {
	q = strings.TrimSpace(q)
	nq := normText(q)
	out := []Card{}
	if nq == "" {
		return out
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.visibleSorted() {
		hit := strings.Contains(normText(t.Title), nq)
		if !hit {
			for _, g := range t.Genres {
				if normText(g) == nq {
					hit = true
				}
			}
		}
		if !hit {
			for _, cs := range t.Cast {
				if strings.Contains(normText(cs.Name), nq) {
					hit = true
					break
				}
			}
		}
		if !hit && normText(t.Category) == nq {
			hit = true
		}
		if !hit {
			for _, a := range t.Aliases {
				if strings.Contains(a, nq) {
					hit = true
					break
				}
			}
		}
		if hit {
			out = append(out, c.card(t, false))
			if len(out) >= 60 {
				break
			}
		}
	}
	return out
}

// ---------- title / episode / file views ----------

type FileLite struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Quality string `json:"quality"`
	Size    int64  `json:"size"`
}

// Entry = ek episode / part / movie ka ek item (alag-alag quality ki files ke saath)
type Entry struct {
	Season  int        `json:"season"`
	Episode int        `json:"episode"`
	Part    int        `json:"part,omitempty"`
	Label   string     `json:"label"`
	Files   []FileLite `json:"files"` // sabse achhi quality pehle
}

type PartView struct {
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	Poster    string `json:"poster,omitempty"`
	Key       string `json:"key,omitempty"`
	Available bool   `json:"available"`
	Current   bool   `json:"current,omitempty"`
}

type TitleView struct {
	Title   *TitleDoc  `json:"title"`
	Entries []Entry    `json:"entries"`
	Parts   []PartView `json:"parts"`
	Similar []Card     `json:"similar"`
	Views   int        `json:"views"`
}

func entryLabel(t *TitleDoc, f *FileDoc) string {
	switch {
	case f.Season > 0 && f.Episode > 0:
		return fmt.Sprintf("S%02d E%02d", f.Season, f.Episode)
	case f.Season > 0:
		return fmt.Sprintf("Season %d", f.Season)
	case f.Episode > 0 && t.Kind == "other":
		return fmt.Sprintf("Part %d", f.Episode)
	case f.Part > 0:
		return fmt.Sprintf("Part %d", f.Part)
	}
	return ""
}

// entries (lock liya hua hona chahiye)
func (c *Catalog) entries(t *TitleDoc) []Entry {
	fs := make([]*FileDoc, 0)
	for _, id := range c.byTitle[t.Key] {
		if f := c.files[id]; f != nil {
			fs = append(fs, f)
		}
	}
	type k struct{ s, e, p int }
	groups := map[k]*Entry{}
	var order []k
	for _, f := range fs {
		key := k{f.Season, f.Episode, 0}
		if f.Season == 0 && f.Episode == 0 {
			key.p = f.Part
		}
		en := groups[key]
		if en == nil {
			en = &Entry{Season: f.Season, Episode: f.Episode, Part: key.p, Label: entryLabel(t, f)}
			groups[key] = en
			order = append(order, key)
		}
		en.Files = append(en.Files, FileLite{ID: f.ID, Name: f.Name, Quality: f.Quality, Size: f.Size})
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.s != b.s {
			return a.s < b.s
		}
		if a.e != b.e {
			return a.e < b.e
		}
		return a.p < b.p
	})
	out := make([]Entry, 0, len(order))
	for _, key := range order {
		en := groups[key]
		sort.SliceStable(en.Files, func(i, j int) bool {
			return rankOf(c.files[en.Files[i].ID]) > rankOf(c.files[en.Files[j].ID])
		})
		out = append(out, *en)
	}
	return out
}

func rankOf(f *FileDoc) int {
	if f == nil {
		return -1
	}
	return f.QRank*1000000 + int(f.Size/1024/1024)
}

func (c *Catalog) partsView(t *TitleDoc) []PartView {
	var pv []PartView
	if len(t.Parts) > 1 {
		for _, p := range t.Parts {
			key := fmt.Sprintf("movie:%d", p.TMDBID)
			v := PartView{Title: p.Title, Year: p.Year, Poster: p.Poster}
			if o := c.titles[key]; o != nil && len(c.byTitle[key]) > 0 {
				v.Key, v.Available = key, true
				if v.Poster == "" {
					v.Poster = o.Poster
				}
			}
			v.Current = key == t.Key
			pv = append(pv, v)
		}
	}
	return pv
}

func (c *Catalog) similar(t *TitleDoc, n int) []Card {
	type sc struct {
		t *TitleDoc
		s int
	}
	var list []sc
	for k, o := range c.titles {
		if k == t.Key || len(c.byTitle[k]) == 0 {
			continue
		}
		s := 0
		if o.Category == t.Category {
			s++
		}
		for _, g := range o.Genres {
			if hasStr(t.Genres, g) {
				s += 2
			}
		}
		if t.CollectionID != 0 && o.CollectionID == t.CollectionID {
			s += 10
		}
		if s > 1 {
			list = append(list, sc{o, s})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].s != list[j].s {
			return list[i].s > list[j].s
		}
		return list[i].t.Key < list[j].t.Key
	})
	out := []Card{}
	for _, x := range list {
		out = append(out, c.card(x.t, false))
		if len(out) >= n {
			break
		}
	}
	return out
}

// trending = sabse zyada dekhe gaye titles (is title ko chhodkar); views nahi to naye titles.
func (c *Catalog) trending(exclude string, n int) []Card {
	all := c.visibleSorted()
	list := make([]*TitleDoc, 0, len(all))
	for _, t := range all {
		if t.Key != exclude {
			list = append(list, t)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Views > list[j].Views })
	out := []Card{}
	for _, t := range list {
		out = append(out, c.card(t, false))
		if len(out) >= n {
			break
		}
	}
	return out
}

func (c *Catalog) TitleView(key string) *TitleView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t := c.titles[key]
	if t == nil || len(c.byTitle[key]) == 0 {
		return nil
	}
	cp := *t
	cp.Premium = c.premium(t)
	return &TitleView{Title: &cp, Entries: c.entries(t), Parts: c.partsView(t), Similar: c.similar(t, 14), Views: t.Views}
}

// FileView = player page ke neeche dikhne wali cheezein.
type FileView struct {
	File     FileLite   `json:"file"`
	Title    *Card      `json:"title,omitempty"`
	Label    string     `json:"label,omitempty"`
	Entries  []Entry    `json:"entries"`   // is title ke saare episode/part
	Current  int        `json:"current"`   // Entries mein abhi wala index
	Next     *FileLite  `json:"next"`      // agla episode/part (isi quality ke aas-paas)
	NextText string     `json:"next_text"` // "S01 E04"
	Qualitys []FileLite `json:"qualities"` // isi episode/movie ki baaki qualities
	Parts    []PartView `json:"parts"`     // movie ke agle-pichle part
	Similar  []Card     `json:"similar"`
	Trending []Card     `json:"trending"`
}

func (c *Catalog) FileView(id int) *FileView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f := c.files[id]
	if f == nil {
		return nil
	}
	fv := &FileView{File: FileLite{ID: f.ID, Name: f.Name, Quality: f.Quality, Size: f.Size}, Entries: []Entry{}, Qualitys: []FileLite{}}
	t := c.titles[f.TitleKey]
	if t == nil {
		fv.Trending = c.trending("", 12)
		return fv
	}
	cd := c.card(t, true)
	fv.Title = &cd
	fv.Label = entryLabel(t, f)
	fv.Entries = c.entries(t)
	fv.Parts = c.partsView(t)
	fv.Similar = c.similar(t, 12)
	fv.Trending = c.trending(t.Key, 12)
	cur := -1
	for i, en := range fv.Entries {
		for _, x := range en.Files {
			if x.ID == id {
				cur = i
			}
		}
	}
	fv.Current = cur
	if cur >= 0 {
		for _, x := range fv.Entries[cur].Files {
			if x.ID != id {
				fv.Qualitys = append(fv.Qualitys, x)
			}
		}
		if cur+1 < len(fv.Entries) && len(fv.Entries) > 1 {
			nx := fv.Entries[cur+1]
			pick := nx.Files[0]
			for _, x := range nx.Files { // wahi quality jo abhi chal rahi hai
				if x.Quality == f.Quality {
					pick = x
					break
				}
			}
			fv.Next = &pick
			fv.NextText = nx.Label
		}
	}
	return fv
}

// ---------- helpers ----------

func hasStr(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func mergeStrings(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]string{a, b} {
		for _, s := range l {
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// ---------- admin (bot) ke liye ----------

type TitleBrief struct {
	Key       string
	Title     string
	Year      int
	Category  string
	Files     int
	FirstFile int
}

// TitleList naye pehle sabhi titles (admin /list ke liye).
func (c *Catalog) TitleList() []TitleBrief {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []TitleBrief
	for _, t := range c.visibleSorted() {
		ids := c.byTitle[t.Key]
		first := 0
		for _, id := range ids {
			if first == 0 || id < first {
				first = id
			}
		}
		out = append(out, TitleBrief{Key: t.Key, Title: t.Title, Year: t.Year, Category: t.Category, Files: len(ids), FirstFile: first})
	}
	return out
}

// GroupOfFile: is file ke title ki saari files ek BatchGroup mein (delete buttons ke liye).
func (c *Catalog) GroupOfFile(fileID int) (BatchGroup, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f := c.files[fileID]
	if f == nil {
		return BatchGroup{}, false
	}
	t := c.titles[f.TitleKey]
	if t == nil {
		return BatchGroup{}, false
	}
	var items []BatchItem
	for _, id := range c.byTitle[t.Key] {
		ff := c.files[id]
		if ff == nil {
			continue
		}
		items = append(items, BatchItem{FileID: ff.ID, Name: ff.Name, Quality: ff.Quality, QRank: ff.QRank, Size: ff.Size,
			TitleKey: t.Key, Title: t.Title, Category: t.Category, Label: entryLabel(t, ff), Year: t.Year, Matched: t.Kind != "other", Rating: t.Rating})
	}
	g := groupBatch(items)
	if len(g) == 0 {
		return BatchGroup{}, false
	}
	return g[0], true
}

func (c *Catalog) AllFileIDs() []int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]int, 0, len(c.files))
	for id := range c.files {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// OtherFiles: jin files ka TMDB match nahi mila ("Other"), naye pehle.
func (c *Catalog) OtherFiles() []FileDoc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []FileDoc
	for _, f := range c.files {
		if t := c.titles[f.TitleKey]; t != nil && t.Kind == "other" {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// SetImage admin ka diya image URL title ke poster/banner ki jagah lagata hai ("" = hatao).
func (c *Catalog) SetImage(ctx context.Context, key, url string) bool {
	c.mu.Lock()
	t := c.titles[key]
	if t == nil {
		c.mu.Unlock()
		return false
	}
	t.Poster, t.Backdrop, t.Updated = url, url, time.Now()
	if url != "" {
		t.Locked = true
	}
	cp := *t
	c.mu.Unlock()
	_ = c.store.SaveTitle(ctx, &cp)
	return true
}

// ---------- premium ----------

// SetPremiumFrom: is saal (aur uske baad) ke saare titles premium (0 = saal ka rule band).
func (c *Catalog) SetPremiumFrom(y int) {
	c.mu.Lock()
	c.premFrom = y
	c.mu.Unlock()
}

func (c *Catalog) PremiumFrom() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.premFrom
}

// premium: caller RLock/Lock pakde ho.
func (c *Catalog) premium(t *TitleDoc) bool {
	switch t.Access {
	case "premium":
		return true
	case "free":
		return false
	}
	return c.premFrom > 0 && t.Year >= c.premFrom
}

// IsPremiumFile: (premium hai?, file mili?)
func (c *Catalog) IsPremiumFile(id int) (bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f := c.files[id]
	if f == nil {
		return false, false
	}
	t := c.titles[f.TitleKey]
	if t == nil {
		return false, true
	}
	return c.premium(t), true
}

// SetAccess: access = "premium" | "free" | "" (auto). Title ka naam wapas deta hai.
func (c *Catalog) SetAccess(ctx context.Context, fileID int, access string) (string, bool) {
	c.mu.Lock()
	f := c.files[fileID]
	if f == nil || c.titles[f.TitleKey] == nil {
		c.mu.Unlock()
		return "", false
	}
	t := c.titles[f.TitleKey]
	t.Access, t.Updated = access, time.Now()
	cp := *t
	c.mu.Unlock()
	_ = c.store.SaveTitle(ctx, &cp)
	return cp.Title, true
}

func (c *Catalog) PremiumTitles() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	for _, t := range c.titles {
		if len(c.byTitle[t.Key]) > 0 && c.premium(t) {
			out = append(out, t.Title)
		}
	}
	sort.Strings(out)
	return out
}
