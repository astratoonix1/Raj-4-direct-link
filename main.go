// AstraToonix NX — Netflix jaisa streaming site.
//
// Admin bot ko video/file bhejta hai
//
//	-> bot storage channel mein forward karta hai
//	-> file ke naam se TMDB mein title dhoondhta hai
//	-> Movie / Series / Anime / Cartoon / Other mein khud baant deta hai
//	-> site par card, poster, rating, cast, parts/episodes sab dikhte hain.
//
// Env variables:
//
//	BOT_TOKEN, API_ID, API_HASH   telegram ke
//	ADMIN_ID                      sirf ye user upload kar sakta hai (comma se kai)
//	CHANNEL_ID                    storage channel (-100...), bot us mein admin ho
//	FQDN                          jaise myapp.koyeb.app
//	DB_URI                        MongoDB ka link
//	TMDB_API_KEY                  themoviedb.org ka key (v3 key ya v4 token)
//	DB_NAME (optional), PORT (optional), SITE_NAME (optional)
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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

//go:embed index.html watch.html
var pages embed.FS

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
	DBURI     string
	DBName    string
	TMDBKey   string
	LogoURL   string
	ThumbURL  string

	GmailUser      string
	GmailPass      string
	AllowedDomains []string
	OwnerName      string
	OwnerAbout     string
	OwnerPhoto     string
	OwnerTelegram  string
	RequireLogin   bool

	StreamConcurrency int
	StreamBufferCount int
	StreamTimeoutSec  int
	StreamMaxRetries  int
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
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
	cfg.DBURI = strings.TrimSpace(firstEnv("DB_URI", "MONGODB_URI", "DATABASE_URL"))
	if cfg.DBURI == "" {
		errs = append(errs, "DB_URI (MongoDB link) missing")
	}
	cfg.DBName = strings.TrimSpace(os.Getenv("DB_NAME"))
	if cfg.DBName == "" {
		cfg.DBName = "astratoonix_nx"
	}
	cfg.TMDBKey = strings.TrimSpace(os.Getenv("TMDB_API_KEY"))
	cfg.LogoURL = strings.TrimSpace(os.Getenv("LOGO_URL"))
	cfg.ThumbURL = strings.TrimSpace(os.Getenv("DEFAULT_THUMB_URL"))
	cfg.GmailUser = strings.TrimSpace(os.Getenv("GMAIL_USER"))
	cfg.GmailPass = strings.TrimSpace(os.Getenv("GMAIL_APP_PASSWORD"))
	doms := strings.TrimSpace(os.Getenv("ALLOWED_EMAIL_DOMAINS"))
	if doms == "" {
		doms = "gmail.com,googlemail.com"
	}
	if doms != "*" {
		for _, d := range strings.Split(doms, ",") {
			if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
				cfg.AllowedDomains = append(cfg.AllowedDomains, d)
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REQUIRE_LOGIN"))) {
	case "1", "true", "on", "yes":
		cfg.RequireLogin = true
	}
	cfg.OwnerName = strings.TrimSpace(os.Getenv("OWNER_NAME"))
	if cfg.OwnerName == "" {
		cfg.OwnerName = "Raj Dev"
	}
	cfg.OwnerAbout = strings.TrimSpace(os.Getenv("OWNER_ABOUT"))
	if cfg.OwnerAbout == "" {
		cfg.OwnerAbout = "Namaste! Main " + cfg.OwnerName + " hoon, is site ka owner. Koi movie ya series chahiye, ya koi dikkat aa rahi hai? Neeche message likho. Main Telegram par padhta hoon aur jawab yahin dikhta hai."
	}
	cfg.OwnerPhoto = strings.TrimSpace(os.Getenv("OWNER_PHOTO_URL"))
	cfg.OwnerTelegram = strings.TrimPrefix(strings.TrimSpace(os.Getenv("OWNER_TELEGRAM")), "@")
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
	cat    *Catalog
	pipe   *Pipeline
	hub    *Hub
	acc    *Accounts
	batch  *batcher
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

func (a *App) servePage(w http.ResponseWriter, name string) {
	b, err := pages.ReadFile(name)
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	(&API{cat: a.cat, siteName: a.cfg.SiteName, logoURL: a.cfg.LogoURL, thumbURL: a.cfg.ThumbURL, extra: a.acc.ExtraConfig}).Register(mux)
	a.acc.Register(mux)
	mux.HandleFunc("/s/", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := parseID(r.URL.Path, "/s/"); ok {
			a.handleStream(w, r, id, false)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/d/", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := parseID(r.URL.Path, "/d/"); ok {
			a.handleStream(w, r, id, true)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/w/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := parseID(r.URL.Path, "/w/"); ok {
			a.servePage(w, "watch.html")
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		a.servePage(w, "index.html")
	})
	return a.acc.GateHandler(mux)
}

// ============================================================
// BOT
// ============================================================

func (a *App) send(chatID int64, text string) {
	text = strings.ToValidUTF8(text, "")
	if r := []rune(text); len(r) > 3900 {
		text = string(r[:3900]) + "..."
	}
	if _, err := a.bot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		a.logger.Warn("send failed", zap.Error(err))
	}
}

// fileMeta message se naam / size nikaalta hai
func fileMeta(m *tgbotapi.Message) (name string, size int64, ok bool) {
	switch {
	case m.Video != nil:
		name, size = m.Video.FileName, int64(m.Video.FileSize)
	case m.Document != nil:
		name, size = m.Document.FileName, int64(m.Document.FileSize)
	case m.Audio != nil:
		name, size = m.Audio.FileName, int64(m.Audio.FileSize)
		if name == "" {
			name = strings.TrimSpace(m.Audio.Performer + " " + m.Audio.Title)
		}
	case m.Animation != nil:
		name, size = m.Animation.FileName, int64(m.Animation.FileSize)
	case m.VideoNote != nil:
		size = int64(m.VideoNote.FileSize)
	case m.Voice != nil:
		size = int64(m.Voice.FileSize)
	default:
		return "", 0, false
	}
	if strings.TrimSpace(name) == "" {
		if c := strings.TrimSpace(strings.SplitN(m.Caption, "\n", 2)[0]); c != "" {
			name = c
		}
	}
	return name, size, true
}

func catFromWord(w string) string {
	switch strings.ToLower(strings.TrimSpace(w)) {
	case "movie", "movies":
		return "Movie"
	case "series", "tv", "show", "shows":
		return "Series"
	case "anime":
		return "Anime"
	case "cartoon", "cartoons":
		return "Cartoon"
	case "other", "others":
		return "Other"
	}
	return ""
}

func (a *App) report(chatID int64, id int, res *Result) {
	if res.Matched {
		year := ""
		if res.Year > 0 {
			year = fmt.Sprintf(" (%d)", res.Year)
		}
		extra := ""
		if res.Label != "" {
			extra = "  " + res.Label
		}
		a.send(chatID, fmt.Sprintf("🎬 %s%s%s\n📂 %s\n\nGalat title ho to:\n/fix %d <tmdb link>", res.Title, year, extra, res.Category, id))
		return
	}
	a.send(chatID, fmt.Sprintf("📁 \"%s\" ka TMDB match nahi mila -> Other mein gaya.\n\nSahi title dena ho to:\n/fix %d <tmdb link>\nYa category badalne ko:\n/cat %d anime|cartoon|series|movie", res.Title, id, id))
}

func (a *App) onMessage(m *tgbotapi.Message) {
	if m.From == nil || m.Chat == nil || m.Chat.Type != "private" {
		return
	}
	if !a.cfg.Admins[m.From.ID] {
		a.send(m.Chat.ID, "Ye bot sirf admin ke liye hai.")
		return
	}
	ctx := context.Background()
	if m.IsCommand() {
		if a.hubCommand(m) {
			return
		}
		a.onCommand(ctx, m)
		return
	}
	if m.ReplyToMessage != nil && a.adminReply(m) {
		return
	}
	name, size, ok := fileMeta(m)
	if !ok {
		a.send(m.Chat.ID, "Video ya file bhejo.")
		return
	}
	fwd, err := a.bot.Send(tgbotapi.NewForward(a.cfg.ChannelID, m.Chat.ID, m.MessageID))
	if err != nil {
		a.logger.Error("forward failed", zap.Error(err))
		a.send(m.Chat.ID, "❌ Storage channel mein nahi bhej paya. Check karo bot us channel mein admin hai aur CHANNEL_ID sahi hai.")
		return
	}
	if name == "" {
		name = fmt.Sprintf("file_%d", fwd.MessageID)
	}
	q, rk := detectQuality(name)
	f := &FileDoc{ID: fwd.MessageID, Name: name, Size: size, Quality: q, QRank: rk, Created: time.Now()}
	if err := a.cat.AddFile(ctx, f); err != nil {
		a.logger.Error("save file failed", zap.Error(err))
	}
	it := BatchItem{FileID: f.ID, Name: name, Quality: q, QRank: rk, Size: size}
	res, err := a.pipe.Process(ctx, f.ID)
	if err != nil {
		a.logger.Error("classify failed", zap.Error(err))
		it.Title, it.Category = name, "Other"
	} else {
		it.TitleKey, it.Title, it.Category, it.Label, it.Year, it.Matched, it.Rating = res.Key, res.Title, res.Category, res.Label, res.Year, res.Matched, res.Rating
	}
	a.batch.add(m.Chat.ID, it)
}

// deleteFile catalog + storage channel dono se file hatata hai.
func (a *App) deleteFile(ctx context.Context, id int) bool {
	if !a.cat.DeleteFile(ctx, id) {
		return false
	}
	a.cache.del(id)
	a.bot.Request(tgbotapi.NewDeleteMessage(a.cfg.ChannelID, id))
	return true
}

// ---------- batch: ek saath aayi files ka ek message ----------

type pendingBatch struct {
	items []BatchItem
	timer *time.Timer
}

type batcher struct {
	a   *App
	mu  sync.Mutex
	per map[int64]*pendingBatch
}

// add: 5 second tak koi nayi file na aaye tab saari files ek message mein bhejta hai.
func (b *batcher) add(chat int64, it BatchItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.per[chat]
	if p == nil {
		p = &pendingBatch{}
		b.per[chat] = p
	}
	p.items = append(p.items, it)
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(5*time.Second, func() { b.flush(chat) })
}

func (b *batcher) flush(chat int64) {
	b.mu.Lock()
	p := b.per[chat]
	delete(b.per, chat)
	b.mu.Unlock()
	if p == nil || len(p.items) == 0 {
		return
	}
	base := "https://" + b.a.cfg.FQDN
	for _, g := range groupBatch(p.items) {
		msg := tgbotapi.NewMessage(chat, g.Text(base))
		msg.DisableWebPagePreview = true
		msg.ReplyMarkup = deleteKeyboard(g)
		if _, err := b.a.bot.Send(msg); err != nil {
			b.a.logger.Warn("batch send failed", zap.Error(err))
		}
	}
}

// deleteKeyboard: har file ke liye ek "🗑" button + poora title hatane ka button.
func deleteKeyboard(g BatchGroup) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	var cur []tgbotapi.InlineKeyboardButton
	for i, it := range g.Items {
		if i >= maxShownPerGroup {
			break
		}
		cur = append(cur, tgbotapi.NewInlineKeyboardButtonData("🗑 "+itemLabel(it), fmt.Sprintf("df:%d", it.FileID)))
		if len(cur) == 2 {
			rows = append(rows, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	if len(g.Items) > 1 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Poora title ("+fmt.Sprint(len(g.Items))+" files) hatao", fmt.Sprintf("dt:%d", g.Items[0].FileID))))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

const listPerPage = 8

// renderList: /list ka ek page — har title ke saath 📂 (files dekho) aur 🗑 (poora title hatao).
func (a *App) renderList(page int) (string, *tgbotapi.InlineKeyboardMarkup) {
	all := a.cat.TitleList()
	if len(all) == 0 {
		return "Abhi koi file nahi hai.", nil
	}
	pages := (len(all) + listPerPage - 1) / listPerPage
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * listPerPage
	end := start + listPerPage
	if end > len(all) {
		end = len(all)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📚 %d titles — page %d/%d\n", len(all), page+1, pages)
	var rows [][]tgbotapi.InlineKeyboardButton
	for i, t := range all[start:end] {
		y := ""
		if t.Year > 0 {
			y = fmt.Sprintf(" (%d)", t.Year)
		}
		fmt.Fprintf(&sb, "\n%d. %s%s · %s · %d file(s)", start+i+1, t.Title, y, t.Category, t.Files)
		short := t.Title
		if r := []rune(short); len(r) > 18 {
			short = string(r[:17]) + "…"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("📂 %d. %s", start+i+1, short), fmt.Sprintf("ls:%d", t.FirstFile)),
			tgbotapi.NewInlineKeyboardButtonData("🗑", fmt.Sprintf("lt:%d:%d", t.FirstFile, page))))
	}
	var nav []tgbotapi.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️ Pehle", fmt.Sprintf("pg:%d", page-1)))
	}
	if page < pages-1 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("Agle ▶️", fmt.Sprintf("pg:%d", page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return strings.ToValidUTF8(sb.String(), ""), &kb
}

const othersPerPage = 5

// renderOthers: Other (TMDB match nahi) files ka ek page — 5 files, har ek ke saath 🗑, neeche Next/Refresh.
func (a *App) renderOthers(page int) (string, *tgbotapi.InlineKeyboardMarkup) {
	all := a.cat.OtherFiles()
	if len(all) == 0 {
		return "✅ Other mein koi file nahi hai.", nil
	}
	pages := (len(all) + othersPerPage - 1) / othersPerPage
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * othersPerPage
	end := start + othersPerPage
	if end > len(all) {
		end = len(all)
	}
	base := "https://" + a.cfg.FQDN
	var sb strings.Builder
	fmt.Fprintf(&sb, "📁 Other files: %d — page %d/%d\n", len(all), page+1, pages)
	var rows [][]tgbotapi.InlineKeyboardButton
	var ids []string
	for i, f := range all[start:end] {
		fmt.Fprintf(&sb, "\n%d. %s\n   %s · %s\n   %s/w/%d\n   🆔 %d\n", start+i+1, f.Name, f.Quality, fmtSizeShort(f.Size), base, f.ID, f.ID)
		short := f.Name
		if r := []rune(short); len(r) > 20 {
			short = string(r[:19]) + "…"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🗑 %d. %s", start+i+1, short), fmt.Sprintf("of:%d:%d", f.ID, page))))
		ids = append(ids, strconv.Itoa(f.ID))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🗑 Ye %d files ek saath hatao", len(ids)), fmt.Sprintf("op:%d:%s", page, strings.Join(ids, ",")))))
	var nav []tgbotapi.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️ Pehle", fmt.Sprintf("on:%d", page-1)))
	}
	nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", fmt.Sprintf("on:%d", page)))
	if page < pages-1 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("Agle ▶️", fmt.Sprintf("on:%d", page+1)))
	}
	rows = append(rows, nav)
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return strings.ToValidUTF8(sb.String(), ""), &kb
}

// deleteAllBackground: catalog turant khaali, storage channel ke messages dheere dheere.
func (a *App) deleteAllBackground(chat int64) {
	ctx := context.Background()
	ids := a.cat.AllFileIDs()
	for _, id := range ids {
		a.cat.DeleteFile(ctx, id)
		a.cache.del(id)
	}
	a.send(chat, fmt.Sprintf("🧹 Site se %d file(s) hat gayi. Ab storage channel saaf ho raha hai…", len(ids)))
	for _, id := range ids {
		a.bot.Request(tgbotapi.NewDeleteMessage(a.cfg.ChannelID, id))
		time.Sleep(60 * time.Millisecond)
	}
	a.send(chat, "✅ Sab kuch hat gaya.")
}

// onCallback: 🗑 button dabane par.
func (a *App) onCallback(cq *tgbotapi.CallbackQuery) {
	if cq.From == nil || !a.cfg.Admins[cq.From.ID] {
		a.bot.Request(tgbotapi.NewCallback(cq.ID, "Sirf admin"))
		return
	}
	if a.hubCallback(cq) {
		return
	}
	ctx := context.Background()
	chat0, mid0 := int64(0), 0
	if cq.Message != nil {
		chat0, mid0 = cq.Message.Chat.ID, cq.Message.MessageID
	}
	p3 := strings.Split(cq.Data, ":")
	switch p3[0] {
	case "pg", "lt":
		page := 0
		if p3[0] == "pg" && len(p3) > 1 {
			page, _ = strconv.Atoi(p3[1])
		}
		if p3[0] == "lt" && len(p3) > 2 {
			fid, _ := strconv.Atoi(p3[1])
			page, _ = strconv.Atoi(p3[2])
			n := 0
			if f := a.cat.File(fid); f != nil {
				for _, x := range a.cat.FilesOfTitle(f.TitleKey) {
					if a.deleteFile(ctx, x) {
						n++
					}
				}
			}
			a.bot.Request(tgbotapi.NewCallback(cq.ID, fmt.Sprintf("🗑 %d file hata di", n)))
		} else {
			a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
		}
		if cq.Message != nil {
			txt, kb := a.renderList(page)
			var em tgbotapi.EditMessageTextConfig
			if kb != nil {
				em = tgbotapi.NewEditMessageTextAndMarkup(chat0, mid0, txt, *kb)
			} else {
				em = tgbotapi.NewEditMessageText(chat0, mid0, txt)
			}
			em.DisableWebPagePreview = true
			a.bot.Request(em)
		}
		return
	case "on", "of", "op":
		page := 0
		note := ""
		switch p3[0] {
		case "on":
			if len(p3) > 1 {
				page, _ = strconv.Atoi(p3[1])
			}
		case "of":
			if len(p3) > 2 {
				fid, _ := strconv.Atoi(p3[1])
				page, _ = strconv.Atoi(p3[2])
				if a.deleteFile(ctx, fid) {
					note = "🗑 Hata diya"
				}
			}
		case "op":
			if len(p3) > 2 {
				page, _ = strconv.Atoi(p3[1])
				n := 0
				for _, x := range strings.Split(p3[2], ",") {
					if fid, err := strconv.Atoi(x); err == nil && a.deleteFile(ctx, fid) {
						n++
					}
				}
				note = fmt.Sprintf("🗑 %d file hata di", n)
			}
		}
		a.bot.Request(tgbotapi.NewCallback(cq.ID, note))
		if cq.Message != nil {
			txt, kb := a.renderOthers(page)
			var em tgbotapi.EditMessageTextConfig
			if kb != nil {
				em = tgbotapi.NewEditMessageTextAndMarkup(chat0, mid0, txt, *kb)
			} else {
				em = tgbotapi.NewEditMessageText(chat0, mid0, txt)
			}
			em.DisableWebPagePreview = true
			a.bot.Request(em)
		}
		return
	case "oall":
		yes := len(p3) > 1 && p3[1] == "1"
		a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
		if cq.Message != nil {
			a.bot.Request(tgbotapi.NewEditMessageReplyMarkup(chat0, mid0, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}))
		}
		if yes {
			n := 0
			for _, f := range a.cat.OtherFiles() {
				if a.deleteFile(ctx, f.ID) {
					n++
				}
			}
			a.send(chat0, fmt.Sprintf("🗑️ Other ki %d file(s) hata di gayi.", n))
		} else {
			a.send(chat0, "Cancel kar diya, kuch nahi hata.")
		}
		return
	case "ls":
		a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
		fid := 0
		if len(p3) > 1 {
			fid, _ = strconv.Atoi(p3[1])
		}
		if g, ok := a.cat.GroupOfFile(fid); ok && cq.Message != nil {
			msg := tgbotapi.NewMessage(chat0, g.Text("https://"+a.cfg.FQDN))
			msg.DisableWebPagePreview = true
			msg.ReplyMarkup = deleteKeyboard(g)
			a.bot.Send(msg)
		}
		return
	case "dall":
		yes := len(p3) > 1 && p3[1] == "1"
		a.bot.Request(tgbotapi.NewCallback(cq.ID, map[bool]string{true: "Shuru…", false: "Cancel"}[yes]))
		if cq.Message != nil {
			a.bot.Request(tgbotapi.NewEditMessageReplyMarkup(chat0, mid0, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}))
		}
		if yes {
			go a.deleteAllBackground(chat0)
		} else {
			a.send(chat0, "Cancel kar diya, kuch nahi hata.")
		}
		return
	}
	parts := strings.SplitN(cq.Data, ":", 2)
	if len(parts) != 2 {
		a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
		return
	}
	id, err := strconv.Atoi(parts[1])
	if err != nil {
		a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
		return
	}
	chat, mid := int64(0), 0
	if cq.Message != nil {
		chat, mid = cq.Message.Chat.ID, cq.Message.MessageID
	}
	switch parts[0] {
	case "df":
		ok := a.deleteFile(ctx, id)
		txt := "🗑 Hata diya"
		if !ok {
			txt = "Pehle hi hata hua hai"
		}
		a.bot.Request(tgbotapi.NewCallback(cq.ID, txt))
		if cq.Message != nil && cq.Message.ReplyMarkup != nil {
			var rows [][]tgbotapi.InlineKeyboardButton
			left := 0
			for _, row := range cq.Message.ReplyMarkup.InlineKeyboard {
				var nr []tgbotapi.InlineKeyboardButton
				for _, bt := range row {
					if bt.CallbackData != nil && *bt.CallbackData == cq.Data {
						continue
					}
					nr = append(nr, bt)
					if bt.CallbackData != nil && strings.HasPrefix(*bt.CallbackData, "df:") {
						left++
					}
				}
				if len(nr) > 0 {
					rows = append(rows, nr)
				}
			}
			if left == 0 {
				rows = nil
			}
			a.bot.Request(tgbotapi.NewEditMessageReplyMarkup(chat, mid, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}))
		}
	case "dt":
		f := a.cat.File(id)
		n := 0
		if f != nil {
			for _, fid := range a.cat.FilesOfTitle(f.TitleKey) {
				if a.deleteFile(ctx, fid) {
					n++
				}
			}
		}
		a.bot.Request(tgbotapi.NewCallback(cq.ID, fmt.Sprintf("🗑 %d file hata di", n)))
		if cq.Message != nil {
			a.bot.Request(tgbotapi.NewEditMessageReplyMarkup(chat, mid, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}))
			a.send(chat, fmt.Sprintf("🗑 %d file(s) hata di gayi.", n))
		}
	default:
		a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
	}
}

func (a *App) onCommand(ctx context.Context, m *tgbotapi.Message) {
	chat := m.Chat.ID
	args := strings.Fields(m.CommandArguments())
	argID := func() (int, bool) {
		if len(args) == 0 {
			return 0, false
		}
		id, err := strconv.Atoi(args[0])
		return id, err == nil && id > 0
	}
	switch m.Command() {
	case "start", "help":
		a.send(chat, "Mujhe koi video ya file bhejo — main uska naam padh kar TMDB se title dhoondh lunga, sahi category mein daal dunga aur link dunga.\n\n"+
			"/fix <id> <tmdb link> — galat title theek karo\n"+
			"/cat <id> movie|series|anime|cartoon|other — category badlo\n"+
			"/retry — Other mein gayi files dobara TMDB se milao\n"+
			"/del <id> — ek file hatao\n"+
			"/deltitle <id> — us title ki saari files (saari quality) hatao\n"+
			"/link <id> — link dobara\n"+
			"/stats — kitna kya hai\n"+
			"/list — saari files/titles ki list (har ek ke saath 🗑 delete)\n"+
			"/others — jinka TMDB match nahi mila unki list (5-5 karke, Next se agla, 🗑 se delete)\n"+
			"/delothers [n] — sabse naye n Other files ek saath hatao (default 5), /delothers all — sab Other\n"+
			"/thumb <id> <image url> — us title ka thumbnail/poster apni image se lagao (/thumb <id> off)\n"+
			"/deleteall — SAB kuch hatao (confirm ke baad)\n"+
			"/tmdb — TMDB connect hai ya nahi, test karo\n"+
			"/id — apna Telegram ID\n\n"+
			"👥 Users & site:\n"+
			"/testmail <email> — Gmail OTP mail jaa rahi hai ya nahi, test karo\n"+
			"/users — saare users (🚫 block, ✅ unblock, 🗑 delete)\n"+
			"/user <email ya id> — ek user ki detail\n"+
			"/block, /unblock, /deluser <email ya id>\n"+
			"/setemail <email ya id> <naya email> — galat email theek karo\n"+
			"/requests — pending movie/series requests\n"+
			"/broadcast <text> — site par sabko update (🔔)\n"+
			"/updates — purane updates (delete ke saath)\n"+
			"User ke message ko Reply karo toh jawab site par dikhta hai.")
	case "id":
		a.send(chat, fmt.Sprintf("Tumhara ID: %d", m.From.ID))
	case "link":
		id, ok := argID()
		if !ok {
			a.send(chat, "Aise likho: /link 123")
			return
		}
		a.send(chat, a.linksText(id))
	case "others":
		txt, kb := a.renderOthers(0)
		msg := tgbotapi.NewMessage(chat, txt)
		msg.DisableWebPagePreview = true
		if kb != nil {
			msg.ReplyMarkup = *kb
		}
		a.bot.Send(msg)
	case "delothers":
		arg := ""
		if len(args) > 0 {
			arg = strings.ToLower(args[0])
		}
		if arg == "all" {
			n := len(a.cat.OtherFiles())
			if n == 0 {
				a.send(chat, "Other mein koi file nahi hai.")
				return
			}
			msg := tgbotapi.NewMessage(chat, fmt.Sprintf("⚠️ Other ki saari %d files hata dun? (Movies/Series/Anime/Cartoon ko kuch nahi hoga.)", n))
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Haan, sab Other hatao", "oall:1"),
				tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "oall:0")))
			a.bot.Send(msg)
			return
		}
		n := 5
		if arg != "" {
			if v, err := strconv.Atoi(arg); err == nil && v > 0 {
				n = v
			}
		}
		if n > 50 {
			n = 50
		}
		others := a.cat.OtherFiles()
		if len(others) == 0 {
			a.send(chat, "Other mein koi file nahi hai.")
			return
		}
		if n > len(others) {
			n = len(others)
		}
		var names []string
		for _, f := range others[:n] {
			if a.deleteFile(ctx, f.ID) {
				names = append(names, "• "+f.Name)
			}
		}
		a.send(chat, fmt.Sprintf("🗑️ %d Other file(s) hata di:\n%s", len(names), strings.Join(names, "\n")))
	case "thumb":
		id, ok := argID()
		if !ok || len(args) < 2 {
			a.send(chat, "Aise likho: /thumb 123 https://example.com/image.jpg\n(hatane ke liye: /thumb 123 off)")
			return
		}
		f := a.cat.File(id)
		if f == nil {
			a.send(chat, "❌ File nahi mili.")
			return
		}
		u := args[1]
		if strings.EqualFold(u, "off") {
			u = ""
		} else if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			a.send(chat, "❌ Image ka link http:// ya https:// se shuru hona chahiye.")
			return
		}
		a.cat.SetImage(ctx, f.TitleKey, u)
		if u == "" {
			a.send(chat, "✅ Custom thumbnail hata diya.")
		} else {
			a.send(chat, "✅ Thumbnail laga diya. Site refresh karke dekho.")
		}
	case "tmdb":
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		title, rating, err := a.pipe.tmdb.Ping(cctx)
		if err != nil {
			a.send(chat, "❌ TMDB connect nahi hua: "+err.Error())
			return
		}
		a.send(chat, fmt.Sprintf("✅ TMDB connected successfully!\n🎬 Test: %s\n⭐ Rating: %.1f/10\n\nAb har upload ka title, poster aur rating khud aayega.", title, rating))
	case "list":
		txt, kb := a.renderList(0)
		msg := tgbotapi.NewMessage(chat, txt)
		msg.DisableWebPagePreview = true
		if kb != nil {
			msg.ReplyMarkup = *kb
		}
		a.bot.Send(msg)
	case "deleteall":
		t, f, _ := a.cat.Stats()
		if f == 0 {
			a.send(chat, "Catalog pehle se khaali hai.")
			return
		}
		msg := tgbotapi.NewMessage(chat, fmt.Sprintf("⚠️ Pakka? %d titles aur %d files site aur storage channel dono se hat jayengi. Ye wapas nahi aayega.", t, f))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Haan, sab hata do", "dall:1"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "dall:0")))
		a.bot.Send(msg)
	case "stats":
		t, f, byCat := a.cat.Stats()
		var keys []string
		for k := range byCat {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		fmt.Fprintf(&sb, "📊 %d titles, %d files\n", t, f)
		for _, k := range keys {
			fmt.Fprintf(&sb, "• %s: %d\n", k, byCat[k])
		}
		a.send(chat, sb.String())
	case "fix":
		id, ok := argID()
		if !ok || len(args) < 2 {
			a.send(chat, "Aise likho: /fix 123 https://www.themoviedb.org/movie/603")
			return
		}
		kind, tid, ok := ParseTMDBLink(strings.Join(args[1:], " "))
		if !ok {
			a.send(chat, "TMDB link samajh nahi aaya. themoviedb.org/movie/... ya /tv/... wala link do.")
			return
		}
		res, moved, err := a.pipe.Fix(ctx, id, kind, tid)
		if err != nil {
			a.send(chat, "❌ "+err.Error())
			return
		}
		a.send(chat, fmt.Sprintf("✅ %s (%d) — %s\n%d file(s) shift ho gayi.", res.Title, res.Year, res.Category, moved))
	case "cat":
		id, ok := argID()
		cat := ""
		if len(args) > 1 {
			cat = catFromWord(args[1])
		}
		if !ok || cat == "" {
			a.send(chat, "Aise likho: /cat 123 anime  (movie | series | anime | cartoon | other)")
			return
		}
		f := a.cat.File(id)
		if f == nil || !a.cat.SetCategory(ctx, f.TitleKey, cat) {
			a.send(chat, "❌ File nahi mili.")
			return
		}
		a.send(chat, "✅ Category: "+cat)
	case "del":
		id, ok := argID()
		if !ok {
			a.send(chat, "Aise likho: /del 123")
			return
		}
		if !a.deleteFile(ctx, id) {
			a.send(chat, "❌ File catalog mein nahi mili.")
			return
		}
		a.send(chat, "🗑️ Hata diya.")
	case "deltitle":
		id, ok := argID()
		if !ok {
			a.send(chat, "Aise likho: /deltitle 123  (123 us title ki koi bhi file ka id)")
			return
		}
		f := a.cat.File(id)
		if f == nil {
			a.send(chat, "❌ File nahi mili.")
			return
		}
		n := 0
		for _, fid := range a.cat.FilesOfTitle(f.TitleKey) {
			if a.deleteFile(ctx, fid) {
				n++
			}
		}
		a.send(chat, fmt.Sprintf("🗑️ %d file(s) hata di gayi.", n))
	case "retry":
		if !a.pipe.tmdb.Enabled() {
			a.send(chat, "❌ TMDB_API_KEY set nahi hai.")
			return
		}
		ids := a.cat.OtherFileIDs()
		a.send(chat, fmt.Sprintf("⏳ %d Other files dobara dekh raha hun...", len(ids)))
		go func() {
			found := 0
			for _, id := range ids {
				cctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
				res, err := a.pipe.Process(cctx, id)
				cancel()
				if err == nil && res.Matched {
					found++
				}
				time.Sleep(300 * time.Millisecond)
			}
			a.send(chat, fmt.Sprintf("✅ Poora hua: %d files ka match mil gaya, %d Other mein hi hain.", found, len(ids)-found))
		}()
	}
}

func (a *App) linksText(id int) string {
	base := "https://" + a.cfg.FQDN
	return fmt.Sprintf("✅ Ready!\n\n▶️ Dekho:\n%s/w/%d\n\n🔗 Direct stream:\n%s/s/%d\n\n⬇️ Download:\n%s/d/%d", base, id, base, id, base, id)
}

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := NewMongoStore(ctx, cfg.DBURI, cfg.DBName)
	if err != nil {
		logger.Fatal("database connect nahi hua", zap.Error(err))
	}
	cat, err := NewCatalog(ctx, store)
	if err != nil {
		logger.Fatal("catalog load nahi hua", zap.Error(err))
	}
	t, f, _ := cat.Stats()
	logger.Info("catalog loaded", zap.Int("titles", t), zap.Int("files", f))

	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		logger.Fatal("bot token galat hai", zap.Error(err))
	}
	logger.Info("bot ready", zap.String("username", "@"+bot.Self.UserName))

	tm := NewTMDB(cfg.TMDBKey)
	if !tm.Enabled() {
		logger.Warn("TMDB_API_KEY set nahi hai — sab files 'Other' mein jayengi")
	}
	app := &App{
		cfg:    cfg,
		bot:    bot,
		mt:     newMTClient(cfg, logger),
		cache:  &infoCache{m: map[int]*fileInfo{}},
		cat:    cat,
		pipe:   NewPipeline(cat, tm),
		logger: logger,
	}
	app.batch = &batcher{a: app, per: map[int64]*pendingBatch{}}

	var mailer Mailer = nopMailer{}
	if cfg.GmailUser != "" && cfg.GmailPass != "" {
		mailer = NewGmailMailer(cfg.GmailUser, cfg.GmailPass, cfg.SiteName)
	} else {
		logger.Warn("GMAIL_USER / GMAIL_APP_PASSWORD set nahi hai — login/OTP band rahega, baaki site chalegi")
	}
	hub, err := NewHub(ctx, store.Docs(), mailer, HubConfig{SiteName: cfg.SiteName, AllowedDomains: cfg.AllowedDomains})
	if err != nil {
		logger.Fatal("hub load nahi hua", zap.Error(err))
	}
	hub.Logf = func(f string, a ...any) { logger.Warn(fmt.Sprintf(f, a...)) }
	hub.Infof = func(f string, a ...any) { logger.Info(fmt.Sprintf(f, a...)) }
	hub.OnRequest = app.notifyRequest
	hub.OnUserMsg = app.notifyUserMsg
	app.hub = hub
	app.acc = &Accounts{Gate: cfg.RequireLogin, hub: hub, cat: cat, tmdb: tm, owner: OwnerInfo{Name: cfg.OwnerName, About: cfg.OwnerAbout, Photo: cfg.OwnerPhoto, Telegram: cfg.OwnerTelegram}}
	if n, b := hub.UserCount(); n > 0 {
		logger.Info("users loaded", zap.Int("users", n), zap.Int("blocked", b), zap.Bool("email", mailer.Enabled()))
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		logger.Info("HTTP server started", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("http server", zap.Error(err))
		}
	}()

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
			if upd.CallbackQuery != nil {
				go func(cq *tgbotapi.CallbackQuery) {
					defer func() {
						if r := recover(); r != nil {
							logger.Error("panic", zap.Any("r", r))
						}
					}()
					app.onCallback(cq)
				}(upd.CallbackQuery)
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
