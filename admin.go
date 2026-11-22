package main

// Admin dashboard: /api/admin/stats (sirf admin).

import (
	"net/http"
	"sort"
	"time"
)

type dashTitle struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Views    int    `json:"views"`
	Files    int    `json:"files"`
}

type dashDay struct {
	Day     string `json:"day"`
	Signups int    `json:"signups"`
}

func (c *Catalog) dashStats(top int) (titles, files int, bytes int64, cats map[string]int, best []dashTitle) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cats = map[string]int{}
	titles, files = len(c.titles), len(c.files)
	for _, f := range c.files {
		bytes += f.Size
	}
	for _, t := range c.titles {
		cats[t.Category]++
		best = append(best, dashTitle{Key: t.Key, Title: t.Title, Category: t.Category, Views: t.Views, Files: len(c.byTitle[t.Key])})
	}
	sort.Slice(best, func(i, j int) bool {
		if best[i].Views != best[j].Views {
			return best[i].Views > best[j].Views
		}
		return best[i].Title < best[j].Title
	})
	if len(best) > top {
		best = best[:top]
	}
	return
}

func (a *Accounts) dashboard() map[string]any {
	h := a.hub
	now := time.Now()
	ist := time.FixedZone("IST", 5*3600+1800)
	mid := istMidnight(now)

	total, blocked := h.UserCount()
	vs := h.VisitStats()
	users := h.ListUsers() // naye pehle
	var newToday, new7, new30 int
	perDay := map[string]int{}
	for _, u := range users {
		perDay[u.Created.In(ist).Format("2006-01-02")]++
	}
	for _, u := range users {
		if u.Created.After(mid) {
			newToday++
		}
		if now.Sub(u.Created) <= 7*24*time.Hour {
			new7++
		}
		if now.Sub(u.Created) <= 30*24*time.Hour {
			new30++
		}
	}
	var days []dashDay
	for i := 13; i >= 0; i-- {
		d := mid.AddDate(0, 0, -i).Format("2006-01-02")
		days = append(days, dashDay{Day: d, Signups: perDay[d]})
	}
	recent := []map[string]any{}
	for i, u := range users {
		if i >= 8 {
			break
		}
		recent = append(recent, map[string]any{"name": u.Name, "email": u.Email, "t": u.Created, "status": a.status(u)})
	}

	subs := h.ActiveSubs()
	lifetime := 0
	for _, u := range subs {
		if u.PremiumUntil.Year() >= 2100 {
			lifetime++
		}
	}
	vips, ticks := 0, 0
	for _, u := range users {
		if a.isVIP(u) && !a.isAdmin(u) {
			vips++
		}
		if a.blueTick(u) {
			ticks++
		}
	}

	// kamai: approved payments
	h.mu.Lock()
	var revAll, rev30, revToday, nPaid int
	for _, p := range h.pays {
		if p.Status != "approved" {
			continue
		}
		nPaid++
		revAll += p.Amount
		when := p.Done
		if when.IsZero() {
			when = p.Created
		}
		if now.Sub(when) <= 30*24*time.Hour {
			rev30 += p.Amount
		}
		if when.After(mid) {
			revToday += p.Amount
		}
	}
	h.mu.Unlock()

	pend := h.PendingPayments()
	pays := []map[string]any{}
	for i, p := range pend {
		if i >= 6 {
			break
		}
		pays = append(pays, map[string]any{"id": p.ID, "name": p.Name, "amount": p.Amount, "months": p.Months, "utr": p.UTR, "t": p.Created})
	}
	reqs := []map[string]any{}
	pr := h.PendingRequests()
	for i, r := range pr {
		if i >= 8 {
			break
		}
		reqs = append(reqs, map[string]any{"id": r.ID, "name": r.Name, "kind": r.Kind, "user": r.UserName, "t": r.Created})
	}

	nt, nf, sz, cats, best := a.cat.dashStats(10)
	return map[string]any{
		"time": now,
		"users": map[string]any{"total": total, "blocked": blocked, "new_today": newToday, "new_7d": new7, "new_30d": new30,
			"vip": vips, "ticks": ticks, "premium_active": len(subs), "lifetime": lifetime, "recent": recent, "per_day": days},
		"visits":   map[string]any{"online": vs.Online, "today": vs.Today, "week": vs.Week, "guests": vs.Guests, "devices": vs.Total},
		"money":    map[string]any{"total": revAll, "last_30d": rev30, "today": revToday, "paid": nPaid, "pending": pays, "pending_n": len(pend)},
		"library":  map[string]any{"titles": nt, "files": nf, "bytes": sz, "cats": cats, "top": best},
		"requests": map[string]any{"pending_n": len(pr), "pending": reqs},
		"system":   map[string]any{"premium": a.premiumOn(), "quality_lock": h.QualityLock(), "ads": h.Ads().On, "ai": h.AIEnabled(), "gemini": a.Gemini != nil},
	}
}

func (a *Accounts) adminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/stats", func(w http.ResponseWriter, r *http.Request) {
		if u := a.user(r); !a.isAdmin(u) {
			fail(w, herr(403, "Admins only."))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, a.dashboard())
	})
}
