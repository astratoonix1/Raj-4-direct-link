package main

// adbot.go — /ad ke Telegram wale hisse (photo bhej kar ad lagana + photo ke bytes laana).

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// fetchTGFile: Telegram photo ke bytes (server side; bot token kabhi bahar nahi jata).
func (a *App) fetchTGFile(fileID string) ([]byte, string, error) {
	u, err := a.bot.GetFileDirectURL(fileID)
	if err != nil {
		return nil, "", err
	}
	c := http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(u)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return data, resp.Header.Get("Content-Type"), err
}

// adPhoto: photo bhejo, caption: /ad text | link
func (a *App) adPhoto(m *tgbotapi.Message) {
	chat := m.Chat.ID
	cap := strings.TrimSpace(m.Caption)
	if i := strings.IndexAny(cap, " \n"); i >= 0 {
		cap = strings.TrimSpace(cap[i:])
	} else {
		cap = ""
	}
	text, link, ok := ParseAdTextLink(cap)
	if !ok {
		a.send(chat, "❌ Link https:// se shuru hona chahiye.\nAise: photo bhejo, caption mein:\n/ad Naya course aa gaya! | https://link")
		return
	}
	fid := m.Photo[len(m.Photo)-1].FileID
	a.hub.SetAd(Ad{On: true, Img: "tg:" + fid, Text: text, URL: link})
	a.send(chat, "✅ Ad lag gaya. Home page par gol popup 5 sec dikhega, 10 sec gayab, phir se.\n/adoff se band, /adon se chalu, /addel se hata do.")
}
