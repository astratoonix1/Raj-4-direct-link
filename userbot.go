package main

// Telegram USER session (string session) se storage channel mein file naam se search.
// Bot channel ki purani history mein search nahi kar sakta, isliye ye alag user client hai.
// Sirf padhta hai (search karta hai): kuch bhejta/delete nahi karta.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	tgsession "github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type userSearcher struct {
	apiID    int
	apiHash  string
	sess     string
	chID     int64  // storage channel (CHANNEL_ID), internal id bina -100 ke
	srcID    int64  // optional: SOURCE channel (0 = files storage channel mein hi hain)
	username string // optional: public channel ka username

	logger *zap.Logger
	ready  atomic.Bool
	mu     sync.Mutex
	api    *tg.Client
	peer   *tg.InputPeerChannel // storage channel
	srcPr  *tg.InputPeerChannel // source channel (source mode mein)
}

func newUserSearcher(cfg *Config, logger *zap.Logger) *userSearcher {
	u := &userSearcher{
		apiID: cfg.APIID, apiHash: cfg.APIHash, sess: strings.TrimSpace(cfg.TGStringSession),
		chID: toInternalChannelID(cfg.ChannelID), username: strings.TrimSpace(cfg.TGChannelUser),
		logger: logger.Named("usersession"),
	}
	if cfg.SourceChannelID != 0 {
		u.srcID = toInternalChannelID(cfg.SourceChannelID)
	}
	go u.run(context.Background())
	return u
}

func (u *userSearcher) Ready() bool      { return u.ready.Load() }
func (u *userSearcher) SourceMode() bool { return u.srcID != 0 }

func (u *userSearcher) run(ctx context.Context) {
	for {
		data, err := tgsession.TelethonSession(u.sess)
		if err != nil {
			u.logger.Error("TG_STRING_SESSION galat hai — Telethon StringSession chahiye (auto-index band)", zap.Error(err))
			return
		}
		storage := new(tgsession.StorageMemory)
		if err := (&tgsession.Loader{Storage: storage}).Save(ctx, data); err != nil {
			u.logger.Error("session load nahi hua", zap.Error(err))
			return
		}
		client := telegram.NewClient(u.apiID, u.apiHash, telegram.Options{
			SessionStorage: storage,
			Logger:         u.logger,
			Middlewares:    []telegram.Middleware{floodwait.NewSimpleWaiter().WithMaxRetries(5)},
		})
		err = client.Run(ctx, func(ctx context.Context) error {
			st, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if !st.Authorized {
				return errors.New("session logged-out hai (naya string session banao)")
			}
			api := tg.NewClient(client)
			peer, err := u.resolvePeer(ctx, api, u.chID, u.username)
			if err != nil {
				return fmt.Errorf("storage channel: %w", err)
			}
			var srcPeer *tg.InputPeerChannel
			if u.srcID != 0 {
				if srcPeer, err = u.resolvePeer(ctx, api, u.srcID, ""); err != nil {
					return fmt.Errorf("source channel: %w", err)
				}
			}
			u.mu.Lock()
			u.api, u.peer, u.srcPr = api, peer, srcPeer
			u.mu.Unlock()
			u.ready.Store(true)
			u.logger.Info("user session ready, channel mil gaya")
			<-ctx.Done()
			return nil
		})
		u.ready.Store(false)
		if ctx.Err() != nil {
			return
		}
		u.logger.Warn("user session reconnect...", zap.Error(err))
		time.Sleep(20 * time.Second)
	}
}

// resolvePeer: channel ka access hash nikalo (username se, ya account ke dialogs se)
func (u *userSearcher) resolvePeer(ctx context.Context, api *tg.Client, chID int64, username string) (*tg.InputPeerChannel, error) {
	if username != "" {
		r, err := api.ContactsResolveUsername(ctx, strings.TrimPrefix(username, "@"))
		if err != nil {
			return nil, fmt.Errorf("username resolve: %w", err)
		}
		for _, c := range r.Chats {
			if ch, ok := c.(*tg.Channel); ok {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
			}
		}
		return nil, errors.New("TG_CHANNEL_USERNAME se channel nahi mila")
	}
	var (
		offDate int
		offID   int
		offPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	)
	for page := 0; page < 10; page++ {
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetDate: offDate, OffsetID: offID, OffsetPeer: offPeer, Limit: 100})
		if err != nil {
			return nil, fmt.Errorf("dialogs: %w", err)
		}
		var (
			dialogs []tg.DialogClass
			msgs    []tg.MessageClass
			chats   []tg.ChatClass
			users   []tg.UserClass
		)
		switch r := res.(type) {
		case *tg.MessagesDialogs:
			dialogs, msgs, chats, users = r.Dialogs, r.Messages, r.Chats, r.Users
		case *tg.MessagesDialogsSlice:
			dialogs, msgs, chats, users = r.Dialogs, r.Messages, r.Chats, r.Users
		default:
			return nil, errors.New("dialogs nahi mile")
		}
		for _, c := range chats {
			if ch, ok := c.(*tg.Channel); ok && ch.ID == chID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
			}
		}
		if len(dialogs) < 100 {
			break
		}
		last, ok := dialogs[len(dialogs)-1].(*tg.Dialog)
		if !ok {
			break
		}
		offID = last.TopMessage
		for _, mc := range msgs {
			switch m := mc.(type) {
			case *tg.Message:
				if m.ID == last.TopMessage && peerKey(m.PeerID) == peerKey(last.Peer) {
					offDate = m.Date
				}
			case *tg.MessageService:
				if m.ID == last.TopMessage && peerKey(m.PeerID) == peerKey(last.Peer) {
					offDate = m.Date
				}
			}
		}
		offPeer = inputPeerFor(last.Peer, chats, users)
		if offPeer == nil {
			break
		}
	}
	return nil, fmt.Errorf("channel %d is account ke chats mein nahi mila. Account ko us channel mein jodo (member/admin banao)", chID)
}

func peerKey(p tg.PeerClass) string {
	switch x := p.(type) {
	case *tg.PeerChannel:
		return fmt.Sprintf("c%d", x.ChannelID)
	case *tg.PeerChat:
		return fmt.Sprintf("g%d", x.ChatID)
	case *tg.PeerUser:
		return fmt.Sprintf("u%d", x.UserID)
	}
	return ""
}

func inputPeerFor(p tg.PeerClass, chats []tg.ChatClass, users []tg.UserClass) tg.InputPeerClass {
	switch x := p.(type) {
	case *tg.PeerChannel:
		for _, c := range chats {
			if ch, ok := c.(*tg.Channel); ok && ch.ID == x.ChannelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: x.ChatID}
	case *tg.PeerUser:
		for _, c := range users {
			if us, ok := c.(*tg.User); ok && us.ID == x.UserID {
				return &tg.InputPeerUser{UserID: us.ID, AccessHash: us.AccessHash}
			}
		}
	}
	return nil
}

// Search: channel mein q se search (3 page tak, 100-100 message)
func (u *userSearcher) Search(ctx context.Context, q string, limit int) ([]ChanMsg, error) {
	api, store, src := u.get()
	peer := store
	if u.srcID != 0 {
		peer = src
	}
	if api == nil || peer == nil {
		return nil, errors.New("user session ready nahi hai")
	}
	var out []ChanMsg
	offsetID := 0
	for page := 0; page < 3; page++ {
		res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer: peer, Q: q, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offsetID, Limit: 100,
		})
		if err != nil {
			return out, err
		}
		var msgs []tg.MessageClass
		switch r := res.(type) {
		case *tg.MessagesMessages:
			msgs = r.Messages
		case *tg.MessagesMessagesSlice:
			msgs = r.Messages
		case *tg.MessagesChannelMessages:
			msgs = r.Messages
		}
		n, lastID := 0, 0
		for _, mc := range msgs {
			m, ok := mc.(*tg.Message)
			if !ok {
				continue
			}
			n++
			lastID = m.ID
			if cm, ok := toChanMsg(m); ok {
				out = append(out, cm)
			}
		}
		if n < 100 || lastID == 0 {
			break
		}
		offsetID = lastID
	}
	return out, nil
}

// toChanMsg: message se file ka naam/size nikalo (filename na ho to caption ki pehli line)
func toChanMsg(m *tg.Message) (ChanMsg, bool) {
	media, ok := m.Media.(*tg.MessageMediaDocument)
	if !ok {
		return ChanMsg{}, false
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return ChanMsg{}, false
	}
	name, isVideo := "", strings.HasPrefix(doc.MimeType, "video/")
	for _, at := range doc.Attributes {
		switch a := at.(type) {
		case *tg.DocumentAttributeFilename:
			name = a.FileName
		case *tg.DocumentAttributeVideo:
			isVideo = true
		}
	}
	if name == "" && isVideo {
		if line := strings.TrimSpace(strings.SplitN(m.Message, "\n", 2)[0]); line != "" {
			name = line + ".mp4"
		}
	}
	if name == "" {
		return ChanMsg{}, false
	}
	return ChanMsg{ID: m.ID, Name: name, Size: doc.Size, Date: time.Unix(int64(m.Date), 0)}, true
}

func (u *userSearcher) get() (*tg.Client, *tg.InputPeerChannel, *tg.InputPeerChannel) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.ready.Load() {
		return nil, nil, nil
	}
	return u.api, u.peer, u.srcPr
}

func randID() int64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return time.Now().UnixNano()
	}
	return n.Int64()
}

// Import: source channel ke messages ko storage channel mein copy (forward, "forwarded from" ke bina) karo.
// Wapas: source message id -> storage message id. Storage channel mein is account ko post karne ka haq (admin) chahiye.
func (u *userSearcher) Import(ctx context.Context, srcIDs []int) (map[int]int, error) {
	api, store, src := u.get()
	if api == nil || store == nil || src == nil {
		return nil, errors.New("user session ready nahi hai")
	}
	out := map[int]int{}
	for i := 0; i < len(srcIDs); i += 40 {
		chunk := srcIDs[i:min(i+40, len(srcIDs))]
		rids := make([]int64, len(chunk))
		byRID := map[int64]int{}
		for j, id := range chunk {
			rids[j] = randID()
			byRID[rids[j]] = id
		}
		upd, err := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
			FromPeer: src, ToPeer: store, ID: chunk, RandomID: rids, DropAuthor: true,
		})
		if err != nil {
			return out, fmt.Errorf("forward: %w", err)
		}
		var ups []tg.UpdateClass
		switch x := upd.(type) {
		case *tg.Updates:
			ups = x.Updates
		case *tg.UpdatesCombined:
			ups = x.Updates
		}
		for _, up := range ups {
			if mid, ok := up.(*tg.UpdateMessageID); ok {
				if srcMsg, ok := byRID[mid.RandomID]; ok {
					out[srcMsg] = mid.ID
				}
			}
		}
		time.Sleep(1500 * time.Millisecond) // flood se bachne ke liye
	}
	return out, nil
}

// NewSince: source channel mein afterID ke baad ke naye file-messages + channel ka sabse naya message ID
func (u *userSearcher) NewSince(ctx context.Context, afterID int) ([]ChanMsg, int, error) {
	api, _, src := u.get()
	if api == nil || src == nil {
		return nil, 0, errors.New("user session ready nahi hai")
	}
	var out []ChanMsg
	top, offsetID := afterID, 0
	for page := 0; page < 4; page++ {
		limit := 100
		if afterID == 0 {
			limit = 1 // pehli baar sirf top message ID chahiye
		}
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: src, OffsetID: offsetID, Limit: limit, MinID: afterID})
		if err != nil {
			return out, top, err
		}
		var msgs []tg.MessageClass
		switch r := res.(type) {
		case *tg.MessagesMessages:
			msgs = r.Messages
		case *tg.MessagesMessagesSlice:
			msgs = r.Messages
		case *tg.MessagesChannelMessages:
			msgs = r.Messages
		}
		n, lastID := 0, 0
		for _, mc := range msgs {
			var id int
			switch m := mc.(type) {
			case *tg.Message:
				id = m.ID
				if cm, ok := toChanMsg(m); ok {
					out = append(out, cm)
				}
			case *tg.MessageService:
				id = m.ID
			default:
				continue
			}
			n++
			lastID = id
			if id > top {
				top = id
			}
		}
		if afterID == 0 || n < limit || lastID == 0 {
			break
		}
		offsetID = lastID
	}
	return out, top, nil
}

// History: channel ke offsetID se purane 100 messages (0 = sabse naye se). Files, kul messages ginti, sabse chhota message ID
func (u *userSearcher) History(ctx context.Context, offsetID int) ([]ChanMsg, int, int, error) {
	api, store, src := u.get()
	peer := store
	if u.srcID != 0 {
		peer = src
	}
	if api == nil || peer == nil {
		return nil, 0, 0, errors.New("user session ready nahi hai")
	}
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offsetID, Limit: 100})
	if err != nil {
		return nil, 0, 0, err
	}
	var msgs []tg.MessageClass
	switch r := res.(type) {
	case *tg.MessagesMessages:
		msgs = r.Messages
	case *tg.MessagesMessagesSlice:
		msgs = r.Messages
	case *tg.MessagesChannelMessages:
		msgs = r.Messages
	}
	var out []ChanMsg
	n, minID := 0, 0
	for _, mc := range msgs {
		var id int
		switch m := mc.(type) {
		case *tg.Message:
			id = m.ID
			if cm, ok := toChanMsg(m); ok {
				out = append(out, cm)
			}
		case *tg.MessageService:
			id = m.ID
		default:
			continue
		}
		n++
		if minID == 0 || id < minID {
			minID = id
		}
	}
	return out, n, minID, nil
}
