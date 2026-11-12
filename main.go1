// Astratoonix Lite — sirf streaming.
// Koi password nahi, koi approval nahi, koi database nahi.
//
//	Admin bot ko video/file bhejta hai
//	  -> bot usse storage channel mein forward karta hai
//	  -> bot link deta hai:  https://<FQDN>/w/<id>   (player page)
//	                         https://<FQDN>/s/<id>   (direct stream)
//	                         https://<FQDN>/d/<id>   (download)
//
// Env variables:
//
//	BOT_TOKEN    (zaroori)  BotFather wala token
//	API_ID       (zaroori)  my.telegram.org se
//	API_HASH     (zaroori)  my.telegram.org se
//	ADMIN_ID     (zaroori)  sirf ye user upload kar sakta hai (comma se kai ids)
//	CHANNEL_ID   (zaroori)  storage channel, jaise -1001234567890 (bot us channel mein admin ho)
//	FQDN         (zaroori)  jaise myapp.koyeb.app  (https:// ke bina)
//	PORT         (optional) default 8080
//	SITE_NAME    (optional) default "AstraToonix"
package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/contrib/middleware/ratelimit"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// ============================================================
// CONFIG
// ============================================================

type Config struct {
	BotToken  string
	APIID     int
	APIHash   string
	Admins    map[int64]bool
	ChannelID int64 // as given (-100...)
	FQDN      string
	Port      string
	SiteName  string

	StreamConcurrency int
	StreamBufferCount int
	StreamTimeoutSec  int
	StreamMaxRetries  int
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		BotToken: strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		APIHash:  strings.TrimSpace(os.Getenv("API_HASH")),
		FQDN:     strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(os.Getenv("FQDN")), "https://"), "http://"), "/"),
		Port:     strings.TrimSpace(os.Getenv("PORT")),
		SiteName: strings.TrimSpace(os.Getenv("SITE_NAME")),
		Admins:   map[int64]bool{},
	}
	var errs []string
	if cfg.BotToken == "" {
		errs = append(errs, "BOT_TOKEN missing")
	}
	if cfg.APIHash == "" {
		errs = append(errs, "API_HASH missing")
	}
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("API_ID"))); err != nil || n <= 0 {
		errs = append(errs, "API_ID missing/invalid")
	} else {
		cfg.APIID = n
	}
	for _, p := range strings.Split(os.Getenv("ADMIN_ID"), ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			cfg.Admins[id] = true
		}
	}
	if len(cfg.Admins) == 0 {
		errs = append(errs, "ADMIN_ID missing/invalid")
	}
	chStr := strings.TrimSpace(os.Getenv("CHANNEL_ID"))
	if chStr == "" {
		chStr = strings.TrimSpace(os.Getenv("DB_CHANNEL_ID"))
	}
	if id, err := strconv.ParseInt(chStr, 10, 64); err != nil || id == 0 {
		errs = append(errs, "CHANNEL_ID missing/invalid")
	} else {
		cfg.ChannelID = id
	}
	if cfg.FQDN == "" {
		errs = append(errs, "FQDN missing")
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.SiteName == "" {
		cfg.SiteName = "AstraToonix"
	}
	cfg.StreamConcurrency = envInt("STREAM_CONCURRENCY", 4)
	cfg.StreamBufferCount = envInt("STREAM_BUFFER_COUNT", 8)
	cfg.StreamTimeoutSec = envInt("STREAM_TIMEOUT_SEC", 30)
	cfg.StreamMaxRetries = envInt("STREAM_MAX_RETRIES", 3)
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return cfg, nil
}

// toInternalChannelID turns -1001234567890 into 1234567890 (what MTProto wants).
func toInternalChannelID(id int64) int64 {
	if id < -1000000000000 {
		return -(id + 1000000000000)
	}
	if id < 0 {
		return -id
	}
	return id
}

// ============================================================
// MTPROTO (Telegram se bytes khinchne wala hissa)
// ============================================================

type mtClient struct {
	api    *tg.Client
	ready  atomic.Bool
	mu     sync.Mutex
	logger *zap.Logger

	chanMu     sync.Mutex
	accessHash map[int64]int64
}

func newMTClient(cfg *Config, logger *zap.Logger) *mtClient {
	m := &mtClient{logger: logger, accessHash: map[int64]int64{}}
	go m.run(context.Background(), cfg)
	return m
}

func (m *mtClient) run(ctx context.Context, cfg *Config) {
	for {
		opts := telegram.Options{
			DCList: dcs.Prod(),
			Logger: m.logger.Named("mtproto"),
			Middlewares: []telegram.Middleware{
				floodwait.NewSimpleWaiter().WithMaxRetries(10),
				ratelimit.New(rate.Every(100*time.Millisecond), 5),
			},
		}
		client := telegram.NewClient(cfg.APIID, cfg.APIHash, opts)
		err := client.Run(ctx, func(ctx context.Context) error {
			if _, err := client.Auth().Bot(ctx, cfg.BotToken); err != nil {
				return fmt.Errorf("bot auth failed: %w", err)
			}
			m.mu.Lock()
			m.api = tg.NewClient(client)
			m.mu.Unlock()
			m.ready.Store(true)
			m.logger.Info("MTProto ready")
			<-ctx.Done()
			return nil
		})
		m.ready.Store(false)
		if ctx.Err() != nil {
			return
		}
		m.logger.Warn("MTProto reconnecting...", zap.Error(err))
		time.Sleep(5 * time.Second)
	}
}

func (m *mtClient) getAPI() *tg.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.api
}

func (m *mtClient) waitReady() bool {
	for i := 0; i < 10; i++ {
		if m.ready.Load() {
			return true
		}
		time.Sleep(time.Second)
	}
	return m.ready.Load()
}

// fileInfo = ek file ki saari zaroori jaankari.
type fileInfo struct {
	Location *tg.InputDocumentFileLocation
	Size     int64
	Name     string
	Mime     string
	IsVideo  bool
	IsAudio  bool
	fetched  time.Time
}

func (m *mtClient) channelAccessHash(ctx context.Context, api *tg.Client, channelID int64) (int64, error) {
	m.chanMu.Lock()
	if h, ok := m.accessHash[channelID]; ok {
		m.chanMu.Unlock()
		return h, nil
	}
	m.chanMu.Unlock()

	result, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: channelID}})
	if err != nil {
		return 0, fmt.Errorf("get channel: %w", err)
	}
	if chats, ok := result.(*tg.MessagesChats); ok {
		for _, chat := range chats.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				m.chanMu.Lock()
				m.accessHash[channelID] = ch.AccessHash
				m.chanMu.Unlock()
				return ch.AccessHash, nil
			}
		}
	}
	return 0, errors.New("channel nahi mila ya bot us channel mein admin nahi hai")
}

func (m *mtClient) fetchFileInfo(ctx context.Context, channelID int64, messageID int) (*fileInfo, error) {
	api := m.getAPI()
	if api == nil || !m.ready.Load() {
		return nil, errors.New("MTProto ready nahi hai")
	}
	hash, err := m.channelAccessHash(ctx, api, channelID)
	if err != nil {
		return nil, err
	}
	msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: hash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}
	var messages []tg.MessageClass
	switch r := msgs.(type) {
	case *tg.MessagesMessages:
		messages = r.Messages
	case *tg.MessagesMessagesSlice:
		messages = r.Messages
	case *tg.MessagesChannelMessages:
		messages = r.Messages
	}
	for _, mc := range messages {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		media, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok {
			continue
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			continue
		}
		fi := &fileInfo{
			Location: &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
			},
			Size:    doc.Size,
			Mime:    doc.MimeType,
			fetched: time.Now(),
		}
		for _, at := range doc.Attributes {
			switch a := at.(type) {
			case *tg.DocumentAttributeFilename:
				fi.Name = a.FileName
			case *tg.DocumentAttributeVideo:
				fi.IsVideo = true
			case *tg.DocumentAttributeAudio:
				fi.IsAudio = true
			}
		}
		if fi.Name == "" {
			fi.Name = fmt.Sprintf("file_%d", messageID)
		}
		if fi.Mime == "" {
			fi.Mime = "application/octet-stream"
		}
		if strings.HasPrefix(fi.Mime, "video/") {
			fi.IsVideo = true
		}
		if strings.HasPrefix(fi.Mime, "audio/") {
			fi.IsAudio = true
		}
		return fi, nil
	}
	return nil, fmt.Errorf("message %d mein koi file nahi mili", messageID)
}

// getFileInfo: 10 minute tak yaad rakhta hai (har Range request par Telegram
// ko dobara na poochna pade). File reference purana na ho isliye zyada der nahi.
type infoCache struct {
	mu sync.Mutex
	m  map[int]*fileInfo
}

func (c *infoCache) get(id int) *fileInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	fi := c.m[id]
	if fi != nil && time.Since(fi.fetched) > 10*time.Minute {
		delete(c.m, id)
		return nil
	}
	return fi
}

func (c *infoCache) set(id int, fi *fileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 500 {
		c.m = map[int]*fileInfo{}
	}
	c.m[id] = fi
}

func (c *infoCache) del(id int) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

// ---- Telegram se bytes padhna (purane project ka wahi chala hua code) ----

func calculateBlockSize(start, end int64) int64 {
	size := end - start + 1
	switch {
	case size < 512*1024:
		return 64 * 1024
	case size < 4*1024*1024:
		return 256 * 1024
	case size < 32*1024*1024:
		return 512 * 1024
	default:
		return 1024 * 1024
	}
}

type TgFileReader struct {
	ctx        context.Context
	cancel     context.CancelFunc
	api        *tg.Client
	cfg        *Config
	location   *tg.InputDocumentFileLocation
	start      int64
	end        int64
	blockSize  int64
	totalBytes int64

	blockQueue   chan []byte
	currentBlock []byte
	blockOffset  int64
	bytesRead    int64

	closeOnce sync.Once
}

func newTgFileReader(ctx context.Context, api *tg.Client, cfg *Config,
	location *tg.InputDocumentFileLocation, start, end int64) *TgFileReader {

	ctx, cancel := context.WithCancel(ctx)
	r := &TgFileReader{
		ctx:        ctx,
		cancel:     cancel,
		api:        api,
		cfg:        cfg,
		location:   location,
		start:      start,
		end:        end,
		blockSize:  calculateBlockSize(start, end),
		totalBytes: end - start + 1,
		blockQueue: make(chan []byte, cfg.StreamBufferCount),
	}
	go r.prefetch()
	return r
}

func (r *TgFileReader) Close() {
	r.closeOnce.Do(func() { r.cancel() })
}

func (r *TgFileReader) Read(p []byte) (n int, err error) {
	if r.bytesRead >= r.totalBytes {
		return 0, io.EOF
	}
	if r.blockOffset >= int64(len(r.currentBlock)) {
		select {
		case block, ok := <-r.blockQueue:
			if !ok {
				if r.bytesRead >= r.totalBytes {
					return 0, io.EOF
				}
				return 0, fmt.Errorf("pipe drained")
			}
			r.currentBlock = block
			r.blockOffset = 0
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	n = copy(p, r.currentBlock[r.blockOffset:])
	r.blockOffset += int64(n)
	r.bytesRead += int64(n)
	return n, nil
}

func (r *TgFileReader) prefetch() {
	defer close(r.blockQueue)

	alignedStart := r.start - (r.start % r.blockSize)
	leftTrim := r.start - alignedStart
	rightTrim := (r.end % r.blockSize) + 1
	totalBlocks := int((r.end - alignedStart + r.blockSize) / r.blockSize)

	currentBlock := 0
	offset := alignedStart

	for currentBlock < totalBlocks {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		batchSize := r.cfg.StreamConcurrency
		if batchSize > totalBlocks-currentBlock {
			batchSize = totalBlocks - currentBlock
		}
		blocks := make([][]byte, batchSize)

		var wg sync.WaitGroup
		var fetchErr error
		var errMu sync.Mutex

		for i := 0; i < batchSize; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				blockNum := currentBlock + idx
				blockOffset := offset + int64(idx)*r.blockSize

				data, err := r.downloadWithRetry(blockOffset)
				if err != nil {
					errMu.Lock()
					if fetchErr == nil {
						fetchErr = err
					}
					errMu.Unlock()
					return
				}

				dataLen := int64(len(data))
				if totalBlocks == 1 {
					if rightTrim > dataLen {
						rightTrim = dataLen
					}
					if leftTrim > dataLen {
						leftTrim = dataLen
					}
					data = data[leftTrim:rightTrim]
				} else if blockNum == 0 {
					if leftTrim > dataLen {
						leftTrim = dataLen
					}
					data = data[leftTrim:]
				} else if blockNum == totalBlocks-1 {
					if dataLen > rightTrim {
						data = data[:rightTrim]
					}
				}
				blocks[idx] = data
			}(i)
		}
		wg.Wait()

		if fetchErr != nil && r.ctx.Err() == nil {
			return
		}
		for _, block := range blocks {
			if block == nil {
				return
			}
			select {
			case r.blockQueue <- block:
			case <-r.ctx.Done():
				return
			}
		}
		currentBlock += batchSize
		offset += r.blockSize * int64(batchSize)
	}
}

func (r *TgFileReader) downloadWithRetry(offset int64) ([]byte, error) {
	backoff := 100 * time.Millisecond
	const maxBackoff = 15 * time.Second
	var lastErr error

	for attempt := 0; attempt < r.cfg.StreamMaxRetries; attempt++ {
		if r.ctx.Err() != nil {
			return nil, r.ctx.Err()
		}
		timeout := time.Duration(r.cfg.StreamTimeoutSec) * time.Second
		ctx, cancel := context.WithTimeout(r.ctx, timeout)
		data, err := r.downloadBlock(ctx, offset)
		cancel()
		if err == nil {
			return data, nil
		}
		lastErr = err
		if r.ctx.Err() != nil {
			return nil, r.ctx.Err()
		}
		select {
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (r *TgFileReader) downloadBlock(ctx context.Context, offset int64) ([]byte, error) {
	res, err := r.api.UploadGetFile(ctx, &tg.UploadGetFileRequest{
		Location: r.location,
		Offset:   offset,
		Limit:    int(r.blockSize),
	})
	if err != nil {
		return nil, err
	}
	switch result := res.(type) {
	case *tg.UploadFile:
		return result.Bytes, nil
	default:
		return nil, fmt.Errorf("unexpected response: %T", res)
	}
}

// ============================================================
// APP + HTTP
// ============================================================

type App struct {
	cfg    *Config
	bot    *tgbotapi.BotAPI
	mt     *mtClient
	cache  *infoCache
	logger *zap.Logger
}

func (a *App) info(ctx context.Context, id int) (*fileInfo, error) {
	if fi := a.cache.get(id); fi != nil {
		return fi, nil
	}
	if !a.mt.waitReady() {
		return nil, errors.New("service start ho rahi hai, thodi der baad try karo")
	}
	fi, err := a.mt.fetchFileInfo(ctx, toInternalChannelID(a.cfg.ChannelID), id)
	if err != nil {
		return nil, err
	}
	a.cache.set(id, fi)
	return fi, nil
}

func parseID(path, prefix string) (int, bool) {
	s := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func safeName(name string) string {
	name = filepath.Base(name)
	name = strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(name)
	if name == "" || name == "." {
		return "file"
	}
	return name
}

func formatSize(b int64) string {
	const u = 1024
	if b < u {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(u), 0
	for n := b / u; n >= u; n /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (a *App) handleStream(w http.ResponseWriter, r *http.Request, id int, download bool) {
	ctx := r.Context()
	fi, err := a.info(ctx, id)
	if err != nil {
		a.logger.Warn("file info failed", zap.Int("id", id), zap.Error(err))
		http.Error(w, "File nahi mili.", http.StatusNotFound)
		return
	}

	size := fi.Size
	start, end := int64(0), size-1
	isRange := false
	if rh := r.Header.Get("Range"); rh != "" && size > 0 {
		parts := strings.SplitN(strings.TrimPrefix(rh, "bytes="), "-", 2)
		if len(parts) == 2 {
			s, e := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			switch {
			case s == "" && e != "": // "-500" = aakhri 500 bytes
				if n, err := strconv.ParseInt(e, 10, 64); err == nil && n > 0 {
					if n > size {
						n = size
					}
					start = size - n
					isRange = true
				}
			default:
				if s != "" {
					start, _ = strconv.ParseInt(s, 10, 64)
				}
				if e != "" {
					end, _ = strconv.ParseInt(e, 10, 64)
				}
				if end >= size {
					end = size - 1
				}
				if start >= 0 && start <= end {
					isRange = true
				}
			}
		}
		if !isRange {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			http.Error(w, "Range galat hai", http.StatusRequestedRangeNotSatisfiable)
			return
		}
	}

	disp := "inline"
	if download {
		disp = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", fi.Mime)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "no-cache")
	h.Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disp, safeName(fi.Name)))
	length := end - start + 1
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	if isRange {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}

	api := a.mt.getAPI()
	if api == nil {
		return
	}
	reader := newTgFileReader(ctx, api, a.cfg, fi.Location, start, end)
	defer reader.Close()
	if _, err := io.CopyN(w, reader, length); err != nil {
		a.logger.Debug("stream ended", zap.Error(err))
		// file reference expire ho gaya ho to agli baar naya le
		a.cache.del(id)
	}
}

func (a *App) handleWatch(w http.ResponseWriter, r *http.Request, id int) {
	fi, err := a.info(r.Context(), id)
	if err != nil {
		http.Error(w, "File nahi mili.", http.StatusNotFound)
		return
	}
	kind := "file"
	switch {
	case fi.IsVideo:
		kind = "video"
	case fi.IsAudio:
		kind = "audio"
	}
	name := html.EscapeString(fi.Name)
	player := ""
	switch kind {
	case "video":
		player = fmt.Sprintf(`<video id="p" controls autoplay playsinline preload="metadata" src="/s/%d"></video>`, id)
	case "audio":
		player = fmt.Sprintf(`<audio id="p" controls autoplay preload="metadata" src="/s/%d" style="width:100%%"></audio>`, id)
	default:
		player = `<div class="nofile">Is file ka preview nahi ho sakta — niche Download dabao.</div>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, watchHTML, name, html.EscapeString(a.cfg.SiteName), player, name, formatSize(fi.Size), id, id)
}

func (a *App) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/" || p == "":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, homeHTML, html.EscapeString(a.cfg.SiteName), html.EscapeString(a.cfg.SiteName))
	case p == "/health" || p == "/healthz":
		w.Write([]byte("ok"))
	case strings.HasPrefix(p, "/w/"):
		if id, ok := parseID(p, "/w/"); ok {
			a.handleWatch(w, r, id)
			return
		}
		http.NotFound(w, r)
	case strings.HasPrefix(p, "/s/"):
		if id, ok := parseID(p, "/s/"); ok {
			a.handleStream(w, r, id, false)
			return
		}
		http.NotFound(w, r)
	case strings.HasPrefix(p, "/d/"):
		if id, ok := parseID(p, "/d/"); ok {
			a.handleStream(w, r, id, true)
			return
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ============================================================
// BOT
// ============================================================

func (a *App) send(chatID int64, text string) {
	if _, err := a.bot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		a.logger.Warn("send failed", zap.Error(err))
	}
}

func hasFile(m *tgbotapi.Message) bool {
	return m.Video != nil || m.Document != nil || m.Audio != nil || m.Animation != nil || m.VideoNote != nil || m.Voice != nil
}

func (a *App) onMessage(m *tgbotapi.Message) {
	if m.From == nil || m.Chat == nil || m.Chat.Type != "private" {
		return
	}
	if !a.cfg.Admins[m.From.ID] {
		a.send(m.Chat.ID, "Ye bot sirf admin ke liye hai.")
		return
	}
	if m.IsCommand() {
		switch m.Command() {
		case "start", "help":
			a.send(m.Chat.ID, "Mujhe koi video ya file bhejo — main uska streaming link de dunga.\n\n/link <id> — kisi purani file ka link dobara\n/id — apna Telegram ID")
		case "id":
			a.send(m.Chat.ID, fmt.Sprintf("Tumhara ID: %d", m.From.ID))
		case "link":
			id, err := strconv.Atoi(strings.TrimSpace(m.CommandArguments()))
			if err != nil || id <= 0 {
				a.send(m.Chat.ID, "Aise likho: /link 123")
				return
			}
			a.send(m.Chat.ID, a.linksText(id))
		}
		return
	}
	if !hasFile(m) {
		a.send(m.Chat.ID, "Video ya file bhejo.")
		return
	}

	fwd, err := a.bot.Send(tgbotapi.NewForward(a.cfg.ChannelID, m.Chat.ID, m.MessageID))
	if err != nil {
		a.logger.Error("forward failed", zap.Error(err))
		a.send(m.Chat.ID, "❌ Storage channel mein nahi bhej paya. Check karo bot us channel mein admin hai aur CHANNEL_ID sahi hai.")
		return
	}
	a.send(m.Chat.ID, a.linksText(fwd.MessageID))
}

func (a *App) linksText(id int) string {
	base := "https://" + a.cfg.FQDN
	return fmt.Sprintf("✅ Ready!\n\n▶️ Dekho:\n%s/w/%d\n\n🔗 Direct stream:\n%s/s/%d\n\n⬇️ Download:\n%s/d/%d", base, id, base, id, base, id)
}

// ============================================================
// PAGES
// ============================================================

const homeHTML = `<!DOCTYPE html><html lang="hi"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b0b10;color:#eee;font-family:system-ui,sans-serif;text-align:center}
h1{font-size:2rem;margin:0 0 8px}p{color:#888;margin:0}</style></head>
<body><div><h1>%s</h1><p>Stream the Universe</p></div></body></html>`

const watchHTML = `<!DOCTYPE html><html lang="hi"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>%s — %s</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#0b0b10;color:#eee;font-family:system-ui,sans-serif}
.wrap{max-width:980px;margin:0 auto;padding:12px}
video,audio{width:100%%;max-height:78vh;background:#000;border-radius:10px;display:block}
.nofile{padding:40px 16px;text-align:center;background:#16161d;border-radius:10px;color:#aaa}
h1{font-size:1.05rem;margin:14px 0 4px;word-break:break-word}
.meta{color:#888;font-size:.85rem;margin-bottom:12px}
.row{display:flex;gap:10px;flex-wrap:wrap}
a.btn,button.btn{background:#e50914;color:#fff;border:0;border-radius:8px;padding:10px 16px;font-size:.95rem;text-decoration:none;cursor:pointer}
a.alt,button.alt{background:#2a2a35}
</style></head><body><div class="wrap">
%s
<h1>%s</h1><div class="meta">%s</div>
<div class="row">
<a class="btn" href="/d/%d">⬇️ Download</a>
<button class="btn alt" id="cp" type="button" data-u="/w/%d">🔗 Link copy</button>
</div></div>
<script>
document.getElementById('cp').onclick=function(){var u=location.origin+this.getAttribute('data-u'),b=this;
(navigator.clipboard?navigator.clipboard.writeText(u):Promise.reject()).then(function(){b.textContent='✅ Copy ho gaya'}).catch(function(){prompt('Link copy karo:',u)});};
</script></body></html>`

// ============================================================
// MAIN
// ============================================================

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		logger.Fatal("bot token galat hai", zap.Error(err))
	}
	logger.Info("bot ready", zap.String("username", "@"+bot.Self.UserName))

	app := &App{
		cfg:    cfg,
		bot:    bot,
		mt:     newMTClient(cfg, logger),
		cache:  &infoCache{m: map[int]*fileInfo{}},
		logger: logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           http.HandlerFunc(app.serveHTTP),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		logger.Info("HTTP server started", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("http server", zap.Error(err))
		}
	}()

	// Bot updates
	bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: false})
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)
	logger.Info("bot polling started")
	for {
		select {
		case <-ctx.Done():
			bot.StopReceivingUpdates()
			shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			srv.Shutdown(shCtx)
			cancel()
			return
		case upd, ok := <-updates:
			if !ok {
				return
			}
			if upd.Message != nil {
				go func(m *tgbotapi.Message) {
					defer func() {
						if r := recover(); r != nil {
							logger.Error("panic", zap.Any("r", r))
						}
					}()
					app.onMessage(m)
				}(upd.Message)
			}
		}
	}
}
