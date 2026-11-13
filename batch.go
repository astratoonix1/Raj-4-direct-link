package main

// batch.go — ek saath aayi (kai quality ki) files ka ek hi message banana. Std library only.

import (
	"fmt"
	"sort"
	"strings"
)

type BatchItem struct {
	FileID   int
	Name     string
	Quality  string
	QRank    int
	Size     int64
	TitleKey string
	Title    string
	Category string
	Label    string
	Year     int
	Matched  bool
	Rating   float64
}

type BatchGroup struct {
	TitleKey string
	Title    string
	Category string
	Year     int
	Matched  bool
	Rating   float64
	Items    []BatchItem
}

func groupBatch(items []BatchItem) []BatchGroup {
	var groups []BatchGroup
	idx := map[string]int{}
	for _, it := range items {
		k := it.TitleKey
		if k == "" {
			k = fmt.Sprintf("?%d", it.FileID)
		}
		i, ok := idx[k]
		if !ok {
			groups = append(groups, BatchGroup{TitleKey: it.TitleKey, Title: it.Title, Category: it.Category, Year: it.Year, Matched: it.Matched, Rating: it.Rating})
			i = len(groups) - 1
			idx[k] = i
		}
		groups[i].Items = append(groups[i].Items, it)
	}
	for gi := range groups {
		its := groups[gi].Items
		sort.SliceStable(its, func(a, b int) bool {
			if its[a].Label != its[b].Label {
				return its[a].Label < its[b].Label
			}
			return its[a].QRank > its[b].QRank
		})
	}
	return groups
}

func fmtSizeShort(b int64) string {
	if b <= 0 {
		return ""
	}
	u := []string{"B", "KB", "MB", "GB"}
	f, i := float64(b), 0
	for f >= 1024 && i < 3 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", b)
	}
	return fmt.Sprintf("%.1f %s", f, u[i])
}

func itemLabel(it BatchItem) string {
	parts := []string{}
	if it.Label != "" {
		parts = append(parts, it.Label)
	}
	parts = append(parts, it.Quality)
	if s := fmtSizeShort(it.Size); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

const maxShownPerGroup = 12

// Text = ek title ke saare naye files ka ek message.
func (g BatchGroup) Text(base string) string {
	var sb strings.Builder
	year := ""
	if g.Year > 0 {
		year = fmt.Sprintf(" (%d)", g.Year)
	}
	if g.Matched {
		fmt.Fprintf(&sb, "🎬 %s%s\n📂 %s\n", g.Title, year, g.Category)
		if g.Rating > 0 {
			fmt.Fprintf(&sb, "⭐ %.1f/10 — ✅ TMDB se juda\n", g.Rating)
		} else {
			sb.WriteString("✅ TMDB se juda\n")
		}
	} else {
		fmt.Fprintf(&sb, "📁 %s%s\n📂 Other (TMDB match nahi mila)\n", g.Title, year)
	}
	fmt.Fprintf(&sb, "📦 %d file(s)\n", len(g.Items))
	for i, it := range g.Items {
		if i >= maxShownPerGroup {
			fmt.Fprintf(&sb, "\n… aur %d file(s)", len(g.Items)-maxShownPerGroup)
			break
		}
		fmt.Fprintf(&sb, "\n▶️ %s\n   %s/w/%d\n   🆔 %d\n", itemLabel(it), base, it.FileID, it.FileID)
	}
	first := g.Items[0].FileID
	if g.Matched {
		fmt.Fprintf(&sb, "\nGalat title ho to: /fix %d <tmdb link>", first)
	} else {
		fmt.Fprintf(&sb, "\nSahi title: /fix %d <tmdb link>\nCategory: /cat %d anime|cartoon|series|movie", first, first)
	}
	return sb.String()
}
