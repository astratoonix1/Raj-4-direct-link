package main

// pipeline.go — nayi file aate hi: naam padho -> TMDB se match -> category tay karo -> title se jodo.
// Match nahi mila to "Other" mein jaati hai.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Pipeline struct {
	cat  *Catalog
	tmdb *TMDB
	mu   sync.Mutex // ek time par ek hi file classify ho (series ke episodes ek hi title banayein)
}

func NewPipeline(cat *Catalog, tmdb *TMDB) *Pipeline { return &Pipeline{cat: cat, tmdb: tmdb} }

// Result = admin ko bhejne wali report
type Result struct {
	Title    string
	Year     int
	Category string
	Label    string
	Matched  bool
	Key      string
}

func aliasKey(pn ParsedName) string {
	n := normText(stripPart(pn.Title))
	if n == "" {
		return ""
	}
	ep := "m"
	if pn.Episodic {
		ep = "t"
	}
	return fmt.Sprintf("%s|%d|%s", n, pn.Year, ep)
}

func prettyTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return s
	}
	words := strings.Fields(s)
	for i, w := range words {
		if w == strings.ToLower(w) && len(w) > 2 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// Process ek file ko classify karke catalog se jod deta hai.
func (p *Pipeline) Process(ctx context.Context, fileID int) (*Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	f := p.cat.File(fileID)
	if f == nil {
		return nil, fmt.Errorf("file %d nahi mili", fileID)
	}
	pn := parseName(f.Name)
	season, episode, part := pn.Season, pn.Episode, pn.Part
	ak := aliasKey(pn)

	// 1) pehle se jaana-pehchana naam?
	if ak != "" {
		if key, ok := p.cat.FindAlias(ak); ok {
			if t := p.cat.Title(key); t != nil {
				return p.finish(ctx, f, t, season, episode, part, true)
			}
		}
	}
	// 2) TMDB
	if p.tmdb.Enabled() {
		if kind, id, ok := p.tmdb.FindMatch(ctx, pn); ok {
			key := fmt.Sprintf("%s:%d", kind, id)
			t := p.cat.Title(key)
			if t == nil {
				fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				td, err := p.tmdb.FetchTitle(fctx, kind, id)
				cancel()
				if err == nil {
					td.Aliases = []string{ak}
					if err := p.cat.UpsertTitle(ctx, td); err != nil {
						return nil, err
					}
					t = p.cat.Title(key)
				}
			} else if ak != "" {
				p.cat.AddAlias(ctx, key, ak)
			}
			if t != nil {
				return p.finish(ctx, f, t, season, episode, part, true)
			}
		}
	}
	// 3) Other
	base := stripPart(pn.Title)
	if base == "" {
		base = strings.TrimSpace(reExt.ReplaceAllString(f.Name, ""))
	}
	okey := "other:" + normText(base)
	if okey == "other:" {
		okey = fmt.Sprintf("other:file%d", f.ID)
	}
	t := p.cat.Title(okey)
	if t == nil {
		nt := &TitleDoc{Key: okey, Kind: "other", Category: "Other", Title: prettyTitle(base), Year: pn.Year}
		if err := p.cat.UpsertTitle(ctx, nt); err != nil {
			return nil, err
		}
		t = p.cat.Title(okey)
	}
	if season == 0 && episode == 0 && part > 0 {
		episode = part // "Part 2" ko Other mein bhi alag entry banao
	}
	return p.finish(ctx, f, t, season, episode, part, false)
}

func (p *Pipeline) finish(ctx context.Context, f *FileDoc, t *TitleDoc, season, episode, part int, matched bool) (*Result, error) {
	if t.Kind == "movie" {
		season, episode = 0, 0 // movie ke liye season/episode ka matlab nahi
	}
	if err := p.cat.Attach(ctx, f.ID, t.Key, season, episode, part); err != nil {
		return nil, err
	}
	nf := p.cat.File(f.ID)
	return &Result{Title: t.Title, Year: t.Year, Category: t.Category, Label: entryLabel(t, nf), Matched: matched && t.Kind != "other", Key: t.Key}, nil
}

// Fix admin ke diye TMDB link se file(on) ka title badalta hai.
// Us file ke saath us title ki baaki files bhi (jaise poori series) saath mein shift ho jaati hain.
func (p *Pipeline) Fix(ctx context.Context, fileID int, kind string, tmdbID int) (*Result, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.cat.File(fileID)
	if f == nil {
		return nil, 0, fmt.Errorf("file %d nahi mili", fileID)
	}
	if !p.tmdb.Enabled() {
		return nil, 0, fmt.Errorf("TMDB_API_KEY set nahi hai")
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	td, err := p.tmdb.FetchTitle(fctx, kind, tmdbID)
	if err != nil {
		return nil, 0, err
	}
	pn := parseName(f.Name)
	if ak := aliasKey(pn); ak != "" {
		td.Aliases = []string{ak}
	}
	td.Locked = true
	oldKey := f.TitleKey
	if err := p.cat.UpsertTitle(ctx, td); err != nil {
		return nil, 0, err
	}
	ids := []int{fileID}
	if oldKey != "" && oldKey != td.Key {
		ids = p.cat.FilesOfTitle(oldKey)
	}
	moved := 0
	for _, id := range ids {
		ff := p.cat.File(id)
		if ff == nil {
			continue
		}
		q := parseName(ff.Name)
		s, e := q.Season, q.Episode
		if td.Kind == "movie" {
			s, e = 0, 0
		}
		if err := p.cat.Attach(ctx, id, td.Key, s, e, q.Part); err == nil {
			moved++
		}
	}
	nt := p.cat.Title(td.Key)
	return &Result{Title: nt.Title, Year: nt.Year, Category: nt.Category, Matched: true, Key: nt.Key}, moved, nil
}
