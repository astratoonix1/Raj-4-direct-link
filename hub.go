package main

// hub.go — users, OTP, sessions, requests, owner messages, updates (std library only).
// Persistence ek chhote Docs interface ke peeche hai (MongoDB: nx_docs). Test mein memory use hoti hai.

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---------- storage ----------

type Docs interface {
	Put(ctx context.Context, coll, id string, data []byte) error
	Delete(ctx context.Context, coll, id string) error
	All(ctx context.Context, coll string) (map[string][]byte, error)
}

const (
	cUsers    = "users"
	cSessions = "sessions"
	cRequests = "requests"
	cMsgs     = "msgs"
	cUpdates  = "updates"
	cPay      = "payments"
	cCodes    = "codes"
	cSettings = "settings"
)

// ---------- types ----------

type User struct {
	ID           string          `json:"id"`
	Email        string          `json:"email"`
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	Salt         []byte          `json:"salt"`
	Hash         []byte          `json:"hash"`
	Blocked      bool            `json:"blocked"`
	Pub          string          `json:"pub,omitempty"` // public ID: NX123456
	BlockedAt    time.Time       `json:"blocked_at,omitempty"`
	BlockedUntil time.Time       `json:"blocked_until,omitempty"` // zero = hamesha
	Dev          []string        `json:"dev,omitempty"`           // device codes
	Created      time.Time       `json:"created"`
	LastLogin    time.Time       `json:"last_login"`
	PremiumUntil time.Time       `json:"premium_until,omitempty"`
	Ver          int             `json:"ver"`
	List         json.RawMessage `json:"list,omitempty"`
	Likes        json.RawMessage `json:"likes,omitempty"`
	CW           json.RawMessage `json:"cw,omitempty"`
}

type session struct {
	UID string    `json:"uid"`
	Exp time.Time `json:"exp"`
}

type Request struct {
	ID       string    `json:"id"`
	Who      string    `json:"who"`
	UserName string    `json:"user_name"`
	Email    string    `json:"email,omitempty"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Year     int       `json:"year,omitempty"`
	Note     string    `json:"note,omitempty"`
	Status   string    `json:"status"` // pending | working | added | rejected
	Created  time.Time `json:"created"`
}

type Msg struct {
	ID   string    `json:"id"`
	Who  string    `json:"who"`
	Name string    `json:"name"`
	Dir  string    `json:"dir"` // "u" = user se, "a" = owner se
	Text string    `json:"text"`
	Auto bool      `json:"auto,omitempty"`
	T    time.Time `json:"t"`
}

type Update struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	T    time.Time `json:"t"`
}

type otpEntry struct {
	Hash     [32]byte
	Exp      time.Time
	Tries    int
	LastSent time.Time
	Sent     []time.Time
	Active   bool
}

type pendingSignup struct {
	Name, Email, Key string
	Salt, Hash       []byte
	OTP              otpEntry
	Created          time.Time
}

type emailChange struct {
	Email, Key string
	OTP        otpEntry
}

// HubErr user ko dikhne wali error (HTTP status ke saath).
type HubErr struct {
	Code int
	Msg  string
}

func (e *HubErr) Error() string { return e.Msg }
func herr(code int, f string, a ...any) *HubErr {
	return &HubErr{Code: code, Msg: fmt.Sprintf(f, a...)}
}

type Plan struct {
	Months int    `json:"months"`
	Price  int    `json:"price"`
	Label  string `json:"label"`
}

type PayReq struct {
	ID      string    `json:"id"`
	UID     string    `json:"uid"`
	Name    string    `json:"name"`
	Email   string    `json:"email"`
	Months  int       `json:"months"`
	Amount  int       `json:"amount"`
	UTR     string    `json:"utr"`
	Status  string    `json:"status"` // pending | approved | rejected
	Created time.Time `json:"created"`
	Done    time.Time `json:"done,omitempty"`
}

type Code struct {
	Code    string    `json:"code"`
	Months  int       `json:"months"`
	UsedBy  string    `json:"used_by,omitempty"`
	Created time.Time `json:"created"`
	UsedAt  time.Time `json:"used_at,omitempty"`
}

type HubConfig struct {
	PremiumDefault bool // pehli baar (jab bot se kuch set nahi hua) premium on hai ya off
	Plans          []Plan
	SiteName       string
	AllowedDomains []string // khaali = koi bhi
}

type Hub struct {
	mu   sync.Mutex
	docs Docs
	mail Mailer
	cfg  HubConfig

	users    map[string]*User // by id
	byKey    map[string]*User // canonical email -> user
	sessions map[string]session
	pending  map[string]*pendingSignup // by canonical email
	resets   map[string]*otpEntry      // by canonical email
	echange  map[string]*emailChange   // by user id
	requests map[string]*Request
	msgs     map[string][]*Msg // by who
	updates  []*Update
	codes    map[string]string // thread code -> who
	premOn   bool
	devBl    map[string]*BlockedDev
	seen     map[string]*DevSeen // device code -> kab aaya
	pubIdx   map[string]string   // public ID -> uid
	ad       Ad
	pays     map[string]*PayReq
	redeems  map[string]*Code

	rl *limiter
	q  chan dbOp

	OnRequest func(r *Request)
	OnUserMsg func(m *Msg, code, email string)
	OnPayment func(p *PayReq)
	Logf      func(format string, a ...any)
	Infof     func(format string, a ...any)
}

type dbOp struct {
	coll, id string
	data     []byte
	del      bool
	done     chan struct{}
}

func NewHub(ctx context.Context, docs Docs, m Mailer, cfg HubConfig) (*Hub, error) {
	h := &Hub{
		docs: docs, mail: m, cfg: cfg,
		devBl: map[string]*BlockedDev{}, seen: map[string]*DevSeen{}, pubIdx: map[string]string{}, users: map[string]*User{}, byKey: map[string]*User{}, sessions: map[string]session{},
		pending: map[string]*pendingSignup{}, resets: map[string]*otpEntry{}, echange: map[string]*emailChange{},
		requests: map[string]*Request{}, msgs: map[string][]*Msg{}, codes: map[string]string{}, pays: map[string]*PayReq{}, redeems: map[string]*Code{},
		rl: newLimiter(), q: make(chan dbOp, 4096),
	}
	load := func(coll string, fn func(id string, b []byte)) error {
		all, err := docs.All(ctx, coll)
		if err != nil {
			return fmt.Errorf("%s load: %w", coll, err)
		}
		for id, b := range all {
			fn(id, b)
		}
		return nil
	}
	if err := load(cUsers, func(id string, b []byte) {
		var u User
		if json.Unmarshal(b, &u) == nil && u.ID != "" {
			h.users[u.ID] = &u
			h.byKey[u.Key] = &u
		}
	}); err != nil {
		return nil, err
	}
	now := time.Now()
	if err := load(cSessions, func(id string, b []byte) {
		var s session
		if json.Unmarshal(b, &s) == nil && s.Exp.After(now) && h.users[s.UID] != nil {
			h.sessions[id] = s
		}
	}); err != nil {
		return nil, err
	}
	if err := load(cRequests, func(id string, b []byte) {
		var r Request
		if json.Unmarshal(b, &r) == nil && r.ID != "" {
			h.requests[r.ID] = &r
			h.codes[ThreadCode(r.Who)] = r.Who
		}
	}); err != nil {
		return nil, err
	}
	if err := load(cMsgs, func(id string, b []byte) {
		var m Msg
		if json.Unmarshal(b, &m) == nil && m.ID != "" {
			h.msgs[m.Who] = append(h.msgs[m.Who], &m)
			h.codes[ThreadCode(m.Who)] = m.Who
		}
	}); err != nil {
		return nil, err
	}
	for _, l := range h.msgs {
		sort.Slice(l, func(i, j int) bool { return l[i].T.Before(l[j].T) })
	}
	if err := load(cUpdates, func(id string, b []byte) {
		var u Update
		if json.Unmarshal(b, &u) == nil && u.ID != "" {
			h.updates = append(h.updates, &u)
		}
	}); err != nil {
		return nil, err
	}
	sort.Slice(h.updates, func(i, j int) bool { return h.updates[i].T.After(h.updates[j].T) })
	if err := load(cPay, func(id string, b []byte) {
		var p PayReq
		if json.Unmarshal(b, &p) == nil && p.ID != "" {
			h.pays[p.ID] = &p
		}
	}); err != nil {
		return nil, err
	}
	if err := load(cCodes, func(id string, b []byte) {
		var c Code
		if json.Unmarshal(b, &c) == nil && c.Code != "" {
			h.redeems[c.Code] = &c
		}
	}); err != nil {
		return nil, err
	}
	h.premOn = cfg.PremiumDefault
	if err := load(cSettings, func(id string, b []byte) {
		var s struct {
			On bool `json:"on"`
		}
		if id == "premium" && json.Unmarshal(b, &s) == nil {
			h.premOn = s.On
		}
		if id == cAdKey {
			json.Unmarshal(b, &h.ad)
		}
	}); err != nil {
		return nil, err
	}
	if err := load(cBlocks, func(id string, b []byte) {
		var d BlockedDev
		if json.Unmarshal(b, &d) == nil && reDevCode.MatchString(d.Code) {
			h.devBl[d.Code] = &d
		}
	}); err != nil {
		return nil, err
	}
	if err := load(cDevs, func(id string, b []byte) {
		var d DevSeen
		if json.Unmarshal(b, &d) == nil && reDevCode.MatchString(d.Code) {
			h.seen[d.Code] = &d
		}
	}); err != nil {
		return nil, err
	}
	go h.writer()
	h.initPubs()
	go h.janitor(ctx)
	return h, nil
}

func (h *Hub) logf(f string, a ...any) {
	if h.Logf != nil {
		h.Logf(f, a...)
	}
}

// ---------- persistence queue ----------

func (h *Hub) writer() {
	for op := range h.q {
		if op.done != nil {
			close(op.done)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var err error
		if op.del {
			err = h.docs.Delete(ctx, op.coll, op.id)
		} else {
			err = h.docs.Put(ctx, op.coll, op.id, op.data)
		}
		cancel()
		if err != nil {
			h.logf("hub save %s/%s: %v", op.coll, op.id, err)
		}
	}
}

func (h *Hub) put(coll, id string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.q <- dbOp{coll: coll, id: id, data: b}
}

func (h *Hub) del(coll, id string) { h.q <- dbOp{coll: coll, id: id, del: true} }

// Flush: ab tak ke saare saves ho jaane tak ruko (tests / shutdown).
func (h *Hub) Flush() {
	d := make(chan struct{})
	h.q <- dbOp{done: d}
	<-d
}

func (h *Hub) janitor(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.sweep()
		}
	}
}

func (h *Hub) sweep() {
	now := time.Now()
	h.mu.Lock()
	h.expireBlocksLocked(now)
	for k, p := range h.pending {
		if now.Sub(p.Created) > 2*time.Hour {
			delete(h.pending, k)
		}
	}
	for k, e := range h.resets {
		if now.After(e.Exp.Add(time.Hour)) {
			delete(h.resets, k)
		}
	}
	for k, e := range h.echange {
		if now.After(e.OTP.Exp.Add(time.Hour)) {
			delete(h.echange, k)
		}
	}
	for k, s := range h.sessions {
		if now.After(s.Exp) {
			delete(h.sessions, k)
			h.del(cSessions, k)
		}
	}
	h.mu.Unlock()
	h.rl.sweep()
}

// ---------- helpers ----------

var reCID = regexp.MustCompile(`^[a-f0-9]{16,32}$`)

func ValidCID(s string) bool { return reCID.MatchString(s) }

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func ThreadCode(who string) string {
	s := sha256.Sum256([]byte("nx-thread:" + who))
	return "T" + hex.EncodeToString(s[:4])
}

func cleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func oneLine(s string, max int) string {
	return strings.Join(strings.Fields(cleanText(s, max)), " ")
}

// CanonEmail: valid email (display, canonical key). Gmail mein dots aur +tag hata kar ek hi mana jaata hai.
func (h *Hub) CanonEmail(raw string) (string, string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) > 120 {
		return "", "", herr(400, "That email is too long.")
	}
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address != raw || strings.ContainsAny(raw, " ,;<>\"") {
		return "", "", herr(400, "Please enter a valid email.")
	}
	at := strings.LastIndex(raw, "@")
	local, domain := raw[:at], raw[at+1:]
	if !strings.Contains(domain, ".") {
		return "", "", herr(400, "Please enter a valid email.")
	}
	if len(h.cfg.AllowedDomains) > 0 {
		ok := false
		for _, d := range h.cfg.AllowedDomains {
			if d == domain {
				ok = true
			}
		}
		if !ok {
			return "", "", herr(400, "Accounts can currently be created only with %s email addresses.", strings.Join(h.cfg.AllowedDomains, " / "))
		}
	}
	key := raw
	if domain == "gmail.com" || domain == "googlemail.com" {
		if i := strings.Index(local, "+"); i >= 0 {
			local = local[:i]
		}
		local = strings.ReplaceAll(local, ".", "")
		if local == "" {
			return "", "", herr(400, "Please enter a valid email.")
		}
		key = local + "@gmail.com"
	}
	return raw, key, nil
}

var hashSem = make(chan struct{}, 3)

const pbkdfIter = 210000

func hashPw(pw string, salt []byte) []byte {
	hashSem <- struct{}{}
	defer func() { <-hashSem }()
	k, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, 32)
	if err != nil {
		return nil
	}
	return k
}

func checkPw(u *User, pw string) bool {
	k := hashPw(pw, u.Salt)
	return k != nil && subtle.ConstantTimeCompare(k, u.Hash) == 1
}

func validName(s string) (string, error) {
	s = oneLine(s, 40)
	if utf8.RuneCountInString(s) < 2 {
		return "", herr(400, "Name must be at least 2 characters.")
	}
	return s, nil
}

func validPw(pw string) error {
	if len(pw) < 8 {
		return herr(400, "Password must be at least 8 characters.")
	}
	if len(pw) > 100 {
		return herr(400, "Password is too long.")
	}
	return nil
}

// ---------- OTP ----------

const (
	otpTTL      = 10 * time.Minute
	otpMaxTries = 5
	otpPerHour  = 6
)

var otpCooldown = 45 * time.Second

func otpHash(salt, code string) [32]byte { return sha256.Sum256([]byte("nx-otp:" + salt + ":" + code)) }

// issue: naya OTP banata hai (cooldown + hourly limit ke saath). Caller lock pakde hue ho.
func issue(e *otpEntry, salt string, now time.Time) (string, error) {
	if !e.LastSent.IsZero() && now.Sub(e.LastSent) < otpCooldown {
		return "", herr(429, "Please wait %d seconds before requesting a new OTP.", int((otpCooldown-now.Sub(e.LastSent)).Seconds())+1)
	}
	keep := e.Sent[:0]
	for _, t := range e.Sent {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	e.Sent = keep
	if len(e.Sent) >= otpPerHour {
		return "", herr(429, "Too many OTPs have been sent to this email. Try again in an hour.")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", herr(500, "Something went wrong. Please try again.")
	}
	code := fmt.Sprintf("%06d", n.Int64())
	e.Hash = otpHash(salt, code)
	e.Exp = now.Add(otpTTL)
	e.Tries = 0
	e.Active = true
	e.LastSent = now
	e.Sent = append(e.Sent, now)
	return code, nil
}

// verify: galat OTP se account kabhi block nahi hota — bas ye OTP bekaar ho jaata hai, naya maang sakte ho.
func verify(e *otpEntry, salt, code string, now time.Time) error {
	code = strings.TrimSpace(code)
	if !e.Active {
		return herr(400, "Please request an OTP first.")
	}
	if now.After(e.Exp) {
		e.Active = false
		return herr(400, "This OTP has expired. Tap \"Resend OTP\".")
	}
	h := otpHash(salt, code)
	if subtle.ConstantTimeCompare(h[:], e.Hash[:]) == 1 {
		e.Active = false
		return nil
	}
	e.Tries++
	if e.Tries >= otpMaxTries {
		e.Active = false
		return herr(400, "Wrong OTP 5 times. This OTP is no longer valid — tap \"Resend OTP\". Your account is not blocked.")
	}
	return herr(400, "Wrong OTP. %d attempts left.", otpMaxTries-e.Tries)
}

func (h *Hub) sendOTP(to, purpose, code string) error {
	if h.mail == nil || !h.mail.Enabled() {
		return herr(503, "The email service is not available right now.")
	}
	subj, text, html := otpMail(h.cfg.SiteName, purpose, code)
	if err := h.mail.Send(to, subj, text, html); err != nil {
		h.logf("otp mail to %s failed: %v", to, err)
		return herr(502, "Could not send the OTP email. Is the address correct? Please try again shortly.")
	}
	if h.Infof != nil {
		h.Infof("otp mail sent to %s (%s)", to, purpose)
	}
	return nil
}

// ---------- signup / login ----------

func (h *Hub) Signup(name, email, pw, ip string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	if err := validPw(pw); err != nil {
		return err
	}
	disp, key, err := h.CanonEmail(email)
	if err != nil {
		return err
	}
	if !h.rl.allow("signup:"+ip, 12, time.Hour) {
		return herr(429, "Too many attempts. Please try again later.")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	hash := hashPw(pw, salt)
	now := time.Now()
	h.mu.Lock()
	if h.byKey[key] != nil {
		h.mu.Unlock()
		return herr(409, "An account already exists for this Gmail. Please sign in (or use \"Forgot password\").")
	}
	p := h.pending[key]
	if p == nil {
		p = &pendingSignup{Created: now}
		h.pending[key] = p
	}
	p.Name, p.Email, p.Key, p.Salt, p.Hash = name, disp, key, salt, hash
	code, err := issue(&p.OTP, "signup:"+key, now)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := h.sendOTP(disp, "signup", code); err != nil {
		h.mu.Lock()
		p.OTP.LastSent = time.Time{}
		p.OTP.Active = false
		h.mu.Unlock()
		return err
	}
	return nil
}

func (h *Hub) ResendSignup(email string) error {
	_, key, err := h.CanonEmail(email)
	if err != nil {
		return err
	}
	h.mu.Lock()
	p := h.pending[key]
	if p == nil {
		h.mu.Unlock()
		return herr(400, "Please fill in the Sign Up form first.")
	}
	code, err := issue(&p.OTP, "signup:"+key, time.Now())
	to := p.Email
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := h.sendOTP(to, "signup", code); err != nil {
		h.mu.Lock()
		p.OTP.LastSent = time.Time{}
		h.mu.Unlock()
		return err
	}
	return nil
}

func (h *Hub) newSession(uid string) string {
	tok := randHex(32)
	s := session{UID: uid, Exp: time.Now().Add(60 * 24 * time.Hour)}
	id := tokID(tok)
	h.sessions[id] = s
	h.put(cSessions, id, s)
	return tok
}

func tokID(tok string) string {
	s := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(s[:])
}

func (h *Hub) VerifySignup(email, code string) (*User, string, error) {
	_, key, err := h.CanonEmail(email)
	if err != nil {
		return nil, "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pending[key]
	if p == nil {
		return nil, "", herr(400, "Signup not found or expired. Please sign up again.")
	}
	if err := verify(&p.OTP, "signup:"+key, code, time.Now()); err != nil {
		return nil, "", err
	}
	if h.byKey[key] != nil {
		delete(h.pending, key)
		return nil, "", herr(409, "An account already exists for this Gmail. Please sign in.")
	}
	u := &User{ID: randHex(4), Email: p.Email, Key: key, Name: p.Name, Salt: p.Salt, Hash: p.Hash,
		Created: time.Now(), LastLogin: time.Now()}
	for h.users[u.ID] != nil {
		u.ID = randHex(4)
	}
	h.assignPubLocked(u)
	h.users[u.ID] = u
	h.byKey[key] = u
	delete(h.pending, key)
	h.put(cUsers, u.ID, u)
	return u, h.newSession(u.ID), nil
}

var dummySalt = []byte("0123456789abcdef")

func (h *Hub) Login(email, pw, ip string) (*User, string, error) {
	_, key, err := h.CanonEmail(email)
	if err != nil {
		return nil, "", herr(401, "Incorrect email or password.")
	}
	if !h.rl.allow("login:"+key, 10, 15*time.Minute) || !h.rl.allow("loginip:"+ip, 40, 15*time.Minute) {
		return nil, "", herr(429, "Too many wrong attempts. Try again in 15 minutes.")
	}
	h.mu.Lock()
	u := h.byKey[key]
	h.mu.Unlock()
	if u == nil {
		hashPw(pw, dummySalt)
		return nil, "", herr(401, "Incorrect email or password.")
	}
	if !checkPw(u, pw) {
		return nil, "", herr(401, "Incorrect email or password.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if u.Blocked {
		return nil, "", herr(403, "Your account is blocked. Please contact the owner (you can send a message from the Contact page without logging in).")
	}
	u.LastLogin = time.Now()
	h.put(cUsers, u.ID, u)
	return u, h.newSession(u.ID), nil
}

func (h *Hub) Logout(tok string) {
	if tok == "" {
		return
	}
	id := tokID(tok)
	h.mu.Lock()
	if _, ok := h.sessions[id]; ok {
		delete(h.sessions, id)
		h.del(cSessions, id)
	}
	h.mu.Unlock()
}

// Auth: cookie token se user (blocked / expired = nil).
func (h *Hub) Auth(tok string) *User {
	if len(tok) != 64 {
		return nil
	}
	id := tokID(tok)
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	if !ok {
		return nil
	}
	if time.Now().After(s.Exp) {
		delete(h.sessions, id)
		h.del(cSessions, id)
		return nil
	}
	u := h.users[s.UID]
	if u == nil || u.Blocked {
		return nil
	}
	return u
}

func (h *Hub) dropSessions(uid string) {
	for id, s := range h.sessions {
		if s.UID == uid {
			delete(h.sessions, id)
			h.del(cSessions, id)
		}
	}
}

// ---------- forgot / reset ----------

func (h *Hub) Forgot(email, ip string) error {
	disp, key, err := h.CanonEmail(email)
	if err != nil {
		return err
	}
	if !h.rl.allow("forgot:"+ip, 10, time.Hour) {
		return herr(429, "Too many attempts. Please try again later.")
	}
	h.mu.Lock()
	u := h.byKey[key]
	var code string
	if u != nil && !u.Blocked {
		e := h.resets[key]
		if e == nil {
			e = &otpEntry{}
			h.resets[key] = e
		}
		code, _ = issue(e, "reset:"+key, time.Now())
		disp = u.Email
	}
	h.mu.Unlock()
	if code != "" {
		h.sendOTP(disp, "reset", code) // user ko pata nahi chalna chahiye account hai ya nahi
	}
	return nil
}

func (h *Hub) Reset(email, code, newPw string) error {
	if err := validPw(newPw); err != nil {
		return err
	}
	_, key, err := h.CanonEmail(email)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	hash := hashPw(newPw, salt)
	h.mu.Lock()
	defer h.mu.Unlock()
	u, e := h.byKey[key], h.resets[key]
	if u == nil || e == nil {
		return herr(400, "The OTP is wrong or expired. Please use \"Forgot password\" again.")
	}
	if err := verify(e, "reset:"+key, code, time.Now()); err != nil {
		return err
	}
	u.Salt, u.Hash = salt, hash
	delete(h.resets, key)
	h.dropSessions(u.ID)
	h.put(cUsers, u.ID, u)
	return nil
}

// ---------- profile: naam / email badalna ----------

func (h *Hub) SetName(uid, name string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.users[uid]; u != nil {
		u.Name = name
		h.put(cUsers, u.ID, u)
	}
	return nil
}

func (h *Hub) StartEmailChange(uid, newEmail, pw string) error {
	disp, key, err := h.CanonEmail(newEmail)
	if err != nil {
		return err
	}
	h.mu.Lock()
	u := h.users[uid]
	h.mu.Unlock()
	if u == nil {
		return herr(401, "Please sign in first.")
	}
	if !h.rl.allow("echg:"+uid, 8, time.Hour) {
		return herr(429, "Too many attempts. Please try again later.")
	}
	if !checkPw(u, pw) {
		return herr(401, "Incorrect password.")
	}
	h.mu.Lock()
	if key == u.Key && disp == u.Email {
		h.mu.Unlock()
		return herr(400, "This is already your current email.")
	}
	if o := h.byKey[key]; o != nil && o.ID != uid {
		h.mu.Unlock()
		return herr(409, "This Gmail is linked to another account.")
	}
	e := h.echange[uid]
	if e == nil {
		e = &emailChange{}
		h.echange[uid] = e
	}
	e.Email, e.Key = disp, key
	code, err := issue(&e.OTP, "echg:"+uid+":"+key, time.Now())
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := h.sendOTP(disp, "email", code); err != nil {
		h.mu.Lock()
		e.OTP.LastSent = time.Time{}
		h.mu.Unlock()
		return err
	}
	return nil
}

func (h *Hub) ConfirmEmailChange(uid, code string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, u := h.echange[uid], h.users[uid]
	if e == nil || u == nil {
		return "", herr(400, "Please enter the new email first.")
	}
	if err := verify(&e.OTP, "echg:"+uid+":"+e.Key, code, time.Now()); err != nil {
		return "", err
	}
	if o := h.byKey[e.Key]; o != nil && o.ID != uid {
		return "", herr(409, "This Gmail is linked to another account.")
	}
	delete(h.byKey, u.Key)
	u.Email, u.Key = e.Email, e.Key
	h.byKey[u.Key] = u
	delete(h.echange, uid)
	h.put(cUsers, u.ID, u)
	return u.Email, nil
}

// ---------- sync (My List / Likes / Continue Watching) ----------

type SyncData struct {
	Ver   int             `json:"ver"`
	List  json.RawMessage `json:"list"`
	Likes json.RawMessage `json:"likes"`
	CW    json.RawMessage `json:"cw"`
}

func rawArr(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("[]")
	}
	return b
}

func (h *Hub) GetSync(uid string) SyncData {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return SyncData{List: json.RawMessage("[]"), Likes: json.RawMessage("[]"), CW: json.RawMessage("[]")}
	}
	return SyncData{Ver: u.Ver, List: rawArr(u.List), Likes: rawArr(u.Likes), CW: rawArr(u.CW)}
}

func isArr(b json.RawMessage, max int) bool {
	if len(b) > max || !json.Valid(b) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(b)), "[")
}

func (h *Hub) PutSync(uid string, list, likes, cw json.RawMessage) (int, error) {
	if !isArr(list, 120000) || !isArr(likes, 60000) || !isArr(cw, 60000) {
		return 0, herr(400, "Invalid data or too large.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return 0, herr(401, "Please sign in first.")
	}
	u.List, u.Likes, u.CW = list, likes, cw
	u.Ver++
	h.put(cUsers, u.ID, u)
	return u.Ver, nil
}

// ---------- requests ----------

var requestKinds = map[string]string{"movie": "Movie", "series": "Series", "anime": "Anime", "cartoon": "Cartoon"}

func (h *Hub) AddRequest(who, userName, email, kind, name string, year int, note, ip string) (*Request, error) {
	k, ok := requestKinds[strings.ToLower(kind)]
	if !ok {
		return nil, herr(400, "Choose a type: Movie / Series / Anime / Cartoon.")
	}
	name = oneLine(name, 100)
	if utf8.RuneCountInString(name) < 2 {
		return nil, herr(400, "Enter the movie / series title.")
	}
	if year != 0 && (year < 1900 || year > time.Now().Year()+2) {
		return nil, herr(400, "Enter a valid year (or leave it empty).")
	}
	note = cleanText(note, 200)
	if !h.rl.allow("req:"+who, 6, time.Hour) || !h.rl.allow("reqip:"+ip, 15, time.Hour) {
		return nil, herr(429, "Too many requests in an hour. Please try again later.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	nn := normText(name)
	for _, r := range h.requests {
		if r.Who == who && r.Status != "rejected" && normText(r.Name) == nn && (r.Status == "pending" || r.Status == "working") {
			return nil, herr(409, "You have already sent this request.")
		}
	}
	r := &Request{ID: randHex(4), Who: who, UserName: userName, Email: email, Kind: k, Name: name, Year: year, Note: note,
		Status: "pending", Created: time.Now()}
	for h.requests[r.ID] != nil {
		r.ID = randHex(4)
	}
	h.requests[r.ID] = r
	h.codes[ThreadCode(who)] = who
	h.put(cRequests, r.ID, r)
	if h.OnRequest != nil {
		go h.OnRequest(r)
	}
	return r, nil
}

func (h *Hub) GetRequest(id string) *Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.requests[id]; r != nil {
		c := *r
		return &c
	}
	return nil
}

// RemoveRequests: user apni purani (pending nahi) requests list se hata sakta hai. id == "all" = sab finished.
func (h *Hub) RemoveRequests(who, id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for rid, r := range h.requests {
		if r.Who == who && r.Status != "pending" && (id == "all" || rid == id) {
			delete(h.requests, rid)
			h.del(cRequests, rid)
			n++
		}
	}
	return n
}

func (h *Hub) RequestsOf(who string) []*Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []*Request{}
	for _, r := range h.requests {
		if r.Who == who {
			c := *r
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

func (h *Hub) PendingRequests() []*Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []*Request{}
	for _, r := range h.requests {
		if r.Status == "pending" || r.Status == "working" {
			c := *r
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

var statusText = map[string]string{
	"added":    "✅ Your request “%s” has been added to the site! Search for it to watch.",
	"working":  "⏳ We are working on “%s” — it will be added soon.",
	"rejected": "❌ “%s” is not available right now. You can try requesting it again later.",
}

func (h *Hub) SetRequestStatus(id, status string) (*Request, bool) {
	tpl, ok := statusText[status]
	if !ok {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.requests[id]
	if r == nil {
		return nil, false
	}
	if r.Status != status {
		r.Status = status
		h.put(cRequests, r.ID, r)
		h.addMsgLocked(r.Who, "Owner", "a", fmt.Sprintf(tpl, r.Name), true)
	}
	c := *r
	return &c, true
}

// ---------- owner messages ----------

func (h *Hub) addMsgLocked(who, name, dir, text string, auto bool) *Msg {
	m := &Msg{ID: randHex(5), Who: who, Name: name, Dir: dir, Text: text, Auto: auto, T: time.Now()}
	h.msgs[who] = append(h.msgs[who], m)
	h.codes[ThreadCode(who)] = who
	h.put(cMsgs, m.ID, m)
	// har thread mein sirf aakhri 200 rakho
	if l := h.msgs[who]; len(l) > 200 {
		for _, o := range l[:len(l)-200] {
			h.del(cMsgs, o.ID)
		}
		h.msgs[who] = l[len(l)-200:]
	}
	return m
}

func (h *Hub) UserMessage(who, name, email, text, ip string) (*Msg, error) {
	text = cleanText(text, 1000)
	if utf8.RuneCountInString(text) < 2 {
		return nil, herr(400, "The message is empty.")
	}
	name = oneLine(name, 40)
	if name == "" {
		name = "Guest"
	}
	if !h.rl.allow("msg:"+who, 8, time.Hour) || !h.rl.allow("msgip:"+ip, 20, time.Hour) {
		return nil, herr(429, "Too many messages in an hour. Please try again later.")
	}
	h.mu.Lock()
	m := h.addMsgLocked(who, name, "u", text, false)
	h.mu.Unlock()
	if h.OnUserMsg != nil {
		go h.OnUserMsg(m, ThreadCode(who), email)
	}
	return m, nil
}

func (h *Hub) WhoByCode(code string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w, ok := h.codes[code]
	return w, ok
}

func (h *Hub) AdminReply(code, text string) (*Msg, error) {
	text = cleanText(text, 1500)
	if text == "" {
		return nil, herr(400, "Reply khaali hai.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	who, ok := h.codes[code]
	if !ok {
		return nil, herr(404, "Ye thread nahi mila.")
	}
	return h.addMsgLocked(who, "Owner", "a", text, false), nil
}

func (h *Hub) Thread(who string) []*Msg {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.msgs[who]
	if len(l) > 100 {
		l = l[len(l)-100:]
	}
	out := make([]*Msg, len(l))
	for i, m := range l {
		c := *m
		out[i] = &c
	}
	return out
}

// ---------- updates / broadcast ----------

func (h *Hub) AddUpdate(text string) (*Update, error) {
	text = cleanText(text, 1000)
	if text == "" {
		return nil, herr(400, "Update khaali hai.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := &Update{ID: randHex(3), Text: text, T: time.Now()}
	h.updates = append([]*Update{u}, h.updates...)
	h.put(cUpdates, u.ID, u)
	for len(h.updates) > 100 {
		h.del(cUpdates, h.updates[len(h.updates)-1].ID)
		h.updates = h.updates[:len(h.updates)-1]
	}
	return u, nil
}

func (h *Hub) DeleteUpdate(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, u := range h.updates {
		if u.ID == id {
			h.updates = append(h.updates[:i:i], h.updates[i+1:]...)
			h.del(cUpdates, id)
			return true
		}
	}
	return false
}

func (h *Hub) Updates(n int) []*Update {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n > len(h.updates) {
		n = len(h.updates)
	}
	out := make([]*Update, n)
	for i := 0; i < n; i++ {
		c := *h.updates[i]
		out[i] = &c
	}
	return out
}

// ---------- admin: users ----------

func (h *Hub) ListUsers() []*User {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*User, 0, len(h.users))
	for _, u := range h.users {
		c := *u
		c.List, c.Likes, c.CW = nil, nil, nil
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// UserByThread: 🧵 code se account wala user (guest ka thread ho toh nil).
func (h *Hub) UserByThread(code string) *User {
	h.mu.Lock()
	who, ok := h.codes[code]
	h.mu.Unlock()
	if !ok || !strings.HasPrefix(who, "u:") {
		return nil
	}
	return h.UserByID(strings.TrimPrefix(who, "u:"))
}

func (h *Hub) UserByID(id string) *User {
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.users[id]; u != nil {
		c := *u
		return &c
	}
	return nil
}

// FindUser: id ya email se (email ki canonical key se bhi milta hai).
func (h *Hub) FindUser(q string) *User {
	q = strings.ToLower(strings.TrimSpace(q))
	if u := h.UserByID(q); u != nil {
		return u
	}
	h.mu.Lock()
	uid := h.pubIdx[strings.ToUpper(q)]
	h.mu.Unlock()
	if uid != "" {
		if u := h.UserByID(uid); u != nil {
			return u
		}
	}
	_, key, err := h.CanonEmail(q)
	if err != nil {
		key = q
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.byKey[key]; u != nil {
		c := *u
		return &c
	}
	return nil
}

func (h *Hub) SetBlocked(id string, b bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.setBlockedLocked(id, b, time.Time{})
}

// BlockUser: until zero = hamesha, warna us waqt auto-unblock.
func (h *Hub) BlockUser(id string, until time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.setBlockedLocked(id, true, until)
}

func (h *Hub) setBlockedLocked(id string, b bool, until time.Time) bool {
	u := h.users[id]
	if u == nil {
		return false
	}
	u.Blocked = b
	if b {
		u.BlockedAt, u.BlockedUntil = time.Now(), until
		h.dropSessions(id)
		for _, d := range u.Dev {
			h.blockDevLocked(d, "user "+u.Pub)
			if x := h.devBl[d]; x != nil {
				x.Until = until
				h.put(cBlocks, d, x)
			}
		}
	} else {
		u.BlockedAt, u.BlockedUntil = time.Time{}, time.Time{}
		for _, d := range u.Dev {
			h.unblockDevLocked(d)
		}
	}
	h.put(cUsers, u.ID, u)
	return true
}

func (h *Hub) DeleteUser(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[id]
	if u == nil {
		return false
	}
	h.dropSessions(id)
	delete(h.users, id)
	delete(h.byKey, u.Key)
	delete(h.echange, id)
	h.del(cUsers, id)
	return true
}

func (h *Hub) AdminSetEmail(id, newEmail string) error {
	disp, key, err := h.CanonEmail(newEmail)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[id]
	if u == nil {
		return herr(404, "User nahi mila.")
	}
	if o := h.byKey[key]; o != nil && o.ID != id {
		return herr(409, "Ye email kisi aur user ka hai.")
	}
	delete(h.byKey, u.Key)
	u.Email, u.Key = disp, key
	h.byKey[key] = u
	h.put(cUsers, u.ID, u)
	return nil
}

func (h *Hub) UserCount() (total, blocked int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, u := range h.users {
		total++
		if u.Blocked {
			blocked++
		}
	}
	return
}

// ---------- rate limiter ----------

type limiter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{m: map[string][]time.Time{}} }

func (l *limiter) allow(key string, max int, win time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := l.m[key]
	keep := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < win {
			keep = append(keep, t)
		}
	}
	if len(keep) >= max {
		l.m[key] = keep
		return false
	}
	l.m[key] = append(keep, now)
	return true
}

func (l *limiter) sweep() {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, ts := range l.m {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > time.Hour {
			delete(l.m, k)
		}
	}
}

// ClearBell: 🔔 mein dikhne wale messages hatata hai. users / requests / movies ko haath nahi lagata.
//
//	mode "auto" = sirf "request add ho gayi" jaise automatic status messages
//	mode "all"  = saare broadcast updates + owner ke saare jawab + status messages
func (h *Hub) ClearBell(mode string) (updates, msgs int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if mode == "all" || mode == "chat" {
		for _, u := range h.updates {
			h.del(cUpdates, u.ID)
		}
		updates = len(h.updates)
		h.updates = nil
	}
	for who, l := range h.msgs {
		keep := l[:0:0]
		for _, m := range l {
			if mode == "chat" || (m.Dir == "a" && (mode == "all" || m.Auto)) {
				h.del(cMsgs, m.ID)
				msgs++
				continue
			}
			keep = append(keep, m)
		}
		if len(keep) == 0 {
			delete(h.msgs, who)
		} else {
			h.msgs[who] = keep
		}
	}
	return
}

// ---------- premium: plans, payments, codes ----------

func (u *User) premiumActive(now time.Time) bool {
	return !u.PremiumUntil.IsZero() && u.PremiumUntil.After(now)
}

// PremiumActive: user ka premium abhi chalu hai?
func (h *Hub) PremiumActive(uid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	return u != nil && u.premiumActive(time.Now())
}

func (h *Hub) PremiumUntil(uid string) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.users[uid]; u != nil {
		return u.PremiumUntil
	}
	return time.Time{}
}

func (h *Hub) PlanFor(months int) (Plan, bool) {
	for _, p := range h.cfg.Plans {
		if p.Months == months {
			return p, true
		}
	}
	return Plan{}, false
}

// grantLocked: abhi chalu premium ke upar months jodta hai.
func (h *Hub) grantLocked(u *User, months int) time.Time {
	base := time.Now()
	if u.PremiumUntil.After(base) {
		base = u.PremiumUntil
	}
	u.PremiumUntil = base.AddDate(0, months, 0)
	h.put(cUsers, u.ID, u)
	return u.PremiumUntil
}

func (h *Hub) notifyUserLocked(uid, text string) {
	h.addMsgLocked("u:"+uid, "Owner", "a", text, true)
}

var reUTR = regexp.MustCompile(`^[A-Z0-9]{8,30}$`)

func (h *Hub) SubmitPayment(uid string, months int, utr, ip string) (*PayReq, error) {
	plan, ok := h.PlanFor(months)
	if !ok {
		return nil, herr(400, "Please choose a valid plan.")
	}
	utr = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(utr), " ", ""))
	if !reUTR.MatchString(utr) {
		return nil, herr(400, "Enter a valid Transaction ID (UTR). It is a 12-digit number in your payment app.")
	}
	if !h.rl.allow("pay:"+uid, 6, time.Hour) || !h.rl.allow("payip:"+ip, 20, time.Hour) {
		return nil, herr(429, "Too many attempts. Please try again later.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return nil, herr(401, "Please sign in first.")
	}
	pending := 0
	for _, p := range h.pays {
		if p.UTR == utr && p.Status != "rejected" {
			return nil, herr(409, "This transaction ID has already been used.")
		}
		if p.UID == uid && p.Status == "pending" {
			pending++
		}
	}
	if pending >= 3 {
		return nil, herr(429, "You already have 3 payments waiting to be verified. Please wait.")
	}
	p := &PayReq{ID: randHex(3), UID: uid, Name: u.Name, Email: u.Email, Months: plan.Months, Amount: plan.Price,
		UTR: utr, Status: "pending", Created: time.Now()}
	for h.pays[p.ID] != nil {
		p.ID = randHex(3)
	}
	h.pays[p.ID] = p
	h.put(cPay, p.ID, p)
	if h.OnPayment != nil {
		go h.OnPayment(p)
	}
	c := *p
	return &c, nil
}

func (h *Hub) GetPayment(id string) *PayReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.pays[id]; p != nil {
		c := *p
		return &c
	}
	return nil
}

func (h *Hub) ApprovePayment(id string) (*PayReq, time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pays[id]
	if p == nil {
		return nil, time.Time{}, herr(404, "Payment request nahi mili.")
	}
	if p.Status != "pending" {
		c := *p
		return &c, time.Time{}, herr(409, "Ye pehle hi %s ho chuki hai.", p.Status)
	}
	u := h.users[p.UID]
	if u == nil {
		return nil, time.Time{}, herr(404, "Ye user ab nahi hai.")
	}
	until := h.grantLocked(u, p.Months)
	p.Status, p.Done = "approved", time.Now()
	h.put(cPay, p.ID, p)
	h.notifyUserLocked(u.ID, fmt.Sprintf("👑 Premium activated! Valid until %s. You can now watch all premium courses.", until.Format("02 Jan 2006")))
	c := *p
	return &c, until, nil
}

func (h *Hub) RejectPayment(id string) (*PayReq, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pays[id]
	if p == nil {
		return nil, herr(404, "Payment request nahi mili.")
	}
	if p.Status != "pending" {
		c := *p
		return &c, herr(409, "Ye pehle hi %s ho chuki hai.", p.Status)
	}
	p.Status, p.Done = "rejected", time.Now()
	h.put(cPay, p.ID, p)
	h.notifyUserLocked(p.UID, fmt.Sprintf("❌ We could not verify the payment for Transaction ID %s. Please resend with the correct ID, or message the owner.", p.UTR))
	c := *p
	return &c, nil
}

func (h *Hub) PendingPayments() []*PayReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*PayReq
	for _, p := range h.pays {
		if p.Status == "pending" {
			c := *p
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// RemovePayment: user apni purani (pending nahi) payment entry list se hata sakta hai.
// id == "all" ho toh saari non-pending hata deta hai.
func (h *Hub) RemovePayment(uid, id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for pid, p := range h.pays {
		if p.UID == uid && p.Status != "pending" && (id == "all" || pid == id) {
			delete(h.pays, pid)
			h.del(cPay, pid)
			n++
		}
	}
	return n
}

func (h *Hub) PaymentsOf(uid string) []*PayReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []*PayReq{}
	for _, p := range h.pays {
		if p.UID == uid {
			c := *p
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func (h *Hub) Grant(uid string, months int) (time.Time, error) {
	if months < 1 || months > 120 {
		return time.Time{}, herr(400, "Mahine 1 se 120 ke beech likho.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return time.Time{}, herr(404, "User nahi mila.")
	}
	until := h.grantLocked(u, months)
	h.notifyUserLocked(uid, fmt.Sprintf("👑 Premium activated! Valid until %s. You can now watch all premium courses.", until.Format("02 Jan 2006")))
	return until, nil
}

func (h *Hub) Revoke(uid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return false
	}
	u.PremiumUntil = time.Time{}
	h.put(cUsers, u.ID, u)
	return true
}

func (h *Hub) ActiveSubs() []*User {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*User
	for _, u := range h.users {
		if u.premiumActive(now) {
			c := *u
			c.List, c.Likes, c.CW = nil, nil, nil
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PremiumUntil.Before(out[j].PremiumUntil) })
	return out
}

const codeAlpha = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	var s strings.Builder
	s.WriteString("AST-")
	for i, x := range b {
		if i == 4 {
			s.WriteByte('-')
		}
		s.WriteByte(codeAlpha[int(x)%len(codeAlpha)])
	}
	return s.String()
}

func (h *Hub) GenCodes(months, n int) ([]string, error) {
	if months < 1 || months > 120 || n < 1 || n > 50 {
		return nil, herr(400, "Mahine 1-120 aur gintI 1-50 rakho.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, n)
	for len(out) < n {
		c := newCode()
		if h.redeems[c] != nil {
			continue
		}
		k := &Code{Code: c, Months: months, Created: time.Now()}
		h.redeems[c] = k
		h.put(cCodes, c, k)
		out = append(out, c)
	}
	return out, nil
}

func (h *Hub) UnusedCodes() []*Code {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*Code
	for _, c := range h.redeems {
		if c.UsedBy == "" {
			cp := *c
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (h *Hub) Redeem(uid, code, ip string) (int, time.Time, error) {
	code = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	if !h.rl.allow("redeem:"+uid, 8, time.Hour) || !h.rl.allow("redeemip:"+ip, 30, time.Hour) {
		return 0, time.Time{}, herr(429, "Too many attempts. Try again in an hour.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return 0, time.Time{}, herr(401, "Please sign in first.")
	}
	c := h.redeems[code]
	if c == nil {
		return 0, time.Time{}, herr(400, "Invalid code.")
	}
	if c.UsedBy != "" {
		return 0, time.Time{}, herr(409, "This code has already been used.")
	}
	c.UsedBy, c.UsedAt = uid, time.Now()
	h.put(cCodes, c.Code, c)
	until := h.grantLocked(u, c.Months)
	return c.Months, until, nil
}

// PremiumEnabled: premium system chalu hai? (bot: /premiumon, /premiumoff)
func (h *Hub) PremiumEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.premOn
}

func (h *Hub) SetPremiumEnabled(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.premOn = on
	h.put(cSettings, "premium", map[string]bool{"on": on})
}
