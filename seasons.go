package main

// Series mein premium season ke release saal se lagta hai (season 1 purana = free, season 4 naya = premium).
// Purani series ke season-saal TMDB se ek baar bhar dene ke liye: startup par apne-aap, aur admin /seasons.

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type seasonSource interface {
	SeasonYears(ctx context.Context, id int) (map[string]int, error)
}

var seasonsRunning int32

// BackfillSeasons: jin tv titles ke season-saal nahi hain unke bharta hai. (bhara, fail) deta hai.
func BackfillSeasons(ctx context.Context, cat *Catalog, src seasonSource, gap time.Duration) (done, failed int) {
	for key, id := range cat.SeriesNeedingSeasons() {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		sy, err := src.SeasonYears(cctx, id)
		cancel()
		if err != nil || len(sy) == 0 || !cat.SetSeasonYears(ctx, key, sy) {
			failed++
		} else {
			done++
		}
		time.Sleep(gap)
	}
	return
}

func (a *App) startSeasonBackfill(say func(string)) bool {
	if a.pipe == nil || !a.pipe.tmdb.Enabled() {
		return false
	}
	if !atomic.CompareAndSwapInt32(&seasonsRunning, 0, 1) {
		return false
	}
	go func() {
		defer atomic.StoreInt32(&seasonsRunning, 0)
		n := len(a.cat.SeriesNeedingSeasons())
		if n == 0 {
			if say != nil {
				say("✅ Sab series ke season-saal pehle se bhare hain.")
			}
			return
		}
		done, failed := BackfillSeasons(context.Background(), a.cat, a.pipe.tmdb, 400*time.Millisecond)
		if say != nil {
			say(fmt.Sprintf("📺 Season-saal bhar diye: %d series theek, %d fail (TMDB id/net).\nAb premium season ke saal se lagega.", done, failed))
		}
	}()
	return true
}

func (a *App) seasonsCommand(m *tgbotapi.Message, arg string) bool {
	if m.Command() != "seasons" {
		return false
	}
	chat := m.Chat.ID
	if a.startSeasonBackfill(func(s string) { a.send(chat, s) }) {
		a.send(chat, fmt.Sprintf("⏳ %d series ke season-saal TMDB se la raha hoon… khatam hone par batata hoon.", len(a.cat.SeriesNeedingSeasons())))
	} else {
		a.send(chat, "Ye abhi chal raha hai ya TMDB key nahi hai.")
	}
	return true
}
