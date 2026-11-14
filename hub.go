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
)

// ---------- types ----------

type User struct {
	ID        string          `json:"id"`
	Email     string          `json:"email"`
	Key       string          `json:"key"`
	Name      string          `json:"name"`
	Salt      []byte          `json:"salt"`
	Hash      []byte          `json:"hash"`
	Blocked   bool            `json:"blocked"`
	Created   time.Time       `json:"created"`
	LastLogin time.Time       `json:"last_login"`
	Ver       int             `json:"ver"`
	List      json.RawMessage `json:"list,omitempty"`
	Likes     json.RawMessage `json:"likes,omitempty"`
	CW        json.RawMessage `json:"cw,omitempty"`
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

type HubConfig struct {
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

	rl *limiter
	q  chan dbOp

	OnRequest func(r *Request)
	OnUserMsg func(m *Msg, code, email string)
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
		users: map[string]*User{}, byKey: map[string]*User{}, sessions: map[string]session{},
		pending: map[string]*pendingSignup{}, resets: map[string]*otpEntry{}, echange: map[string]*emailChange{},
		requests: map[string]*Request{}, msgs: map[string][]*Msg{}, codes: map[string]string{},
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
	go h.writer()
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
		return "", "", herr(400, "Email bahut lamba hai.")
	}
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address != raw || strings.ContainsAny(raw, " ,;<>\"") {
		return "", "", herr(400, "Sahi email daalo.")
	}
	at := strings.LastIndex(raw, "@")
	local, domain := raw[:at], raw[at+1:]
	if !strings.Contains(domain, ".") {
		return "", "", herr(400, "Sahi email daalo.")
	}
	if len(h.cfg.AllowedDomains) > 0 {
		ok := false
		for _, d := range h.cfg.AllowedDomains {
			if d == domain {
				ok = true
			}
		}
		if !ok {
			return "", "", herr(400, "Abhi sirf %s email se account ban sakta hai.", strings.Join(h.cfg.AllowedDomains, " / "))
		}
	}
	key := raw
	if domain == "gmail.com" || domain == "googlemail.com" {
		if i := strings.Index(local, "+"); i >= 0 {
			local = local[:i]
		}
		local = strings.ReplaceAll(local, ".", "")
		if local == "" {
			return "", "", herr(400, "Sahi email daalo.")
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
		return "", herr(400, "Naam kam se kam 2 akshar ka rakho.")
	}
	return s, nil
}

func validPw(pw string) error {
	if len(pw) < 8 {
		return herr(400, "Password kam se kam 8 akshar ka rakho.")
	}
	if len(pw) > 100 {
		return herr(400, "Password bahut lamba hai.")
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
		return "", herr(429, "Thoda ruko, %d second baad naya OTP maango.", int((otpCooldown-now.Sub(e.LastSent)).Seconds())+1)
	}
	keep := e.Sent[:0]
	for _, t := range e.Sent {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	e.Sent = keep
	if len(e.Sent) >= otpPerHour {
		return "", herr(429, "Is email par bahut OTP bheje ja chuke hain. Ek ghante baad try karo.")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", herr(500, "Dikkat aayi, dobara try karo.")
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
		return herr(400, "Pehle OTP maango.")
	}
	if now.After(e.Exp) {
		e.Active = false
		return herr(400, "OTP ki 10 minute ki limit khatam. \"Naya OTP bhejo\" dabao.")
	}
	h := otpHash(salt, code)
	if subtle.ConstantTimeCompare(h[:], e.Hash[:]) == 1 {
		e.Active = false
		return nil
	}
	e.Tries++
	if e.Tries >= otpMaxTries {
		e.Active = false
		return herr(400, "5 baar galat OTP. Ye OTP band ho gaya — \"Naya OTP bhejo\" dabao, account block nahi hua.")
	}
	return herr(400, "OTP galat hai. %d koshish baaki.", otpMaxTries-e.Tries)
}

func (h *Hub) sendOTP(to, purpose, code string) error {
	if h.mail == nil || !h.mail.Enabled() {
		return herr(503, "Email service abhi chalu nahi hai.")
	}
	subj, text, html := otpMail(h.cfg.SiteName, purpose, code)
	if err := h.mail.Send(to, subj, text, html); err != nil {
		h.logf("otp mail to %s failed: %v", to, err)
		return herr(502, "OTP email nahi bhej paya. Email sahi hai? Thodi der baad try karo.")
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
		return herr(429, "Bahut zyada koshish. Thodi der baad aao.")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	hash := hashPw(pw, salt)
	now := time.Now()
	h.mu.Lock()
	if h.byKey[key] != nil {
		h.mu.Unlock()
		return herr(409, "Is Gmail se account pehle se bana hua hai. Sign In karo (password bhool gaye ho toh \"Forgot password\").")
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
		return herr(400, "Pehle Sign Up form bharo.")
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
		return nil, "", herr(400, "Signup nahi mila ya purana ho gaya. Dobara Sign Up karo.")
	}
	if err := verify(&p.OTP, "signup:"+key, code, time.Now()); err != nil {
		return nil, "", err
	}
	if h.byKey[key] != nil {
		delete(h.pending, key)
		return nil, "", herr(409, "Is Gmail se account pehle se hai. Sign In karo.")
	}
	u := &User{ID: randHex(4), Email: p.Email, Key: key, Name: p.Name, Salt: p.Salt, Hash: p.Hash,
		Created: time.Now(), LastLogin: time.Now()}
	for h.users[u.ID] != nil {
		u.ID = randHex(4)
	}
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
		return nil, "", herr(401, "Email ya password galat hai.")
	}
	if !h.rl.allow("login:"+key, 10, 15*time.Minute) || !h.rl.allow("loginip:"+ip, 40, 15*time.Minute) {
		return nil, "", herr(429, "Bahut baar galat koshish. 15 minute baad dobara try karo.")
	}
	h.mu.Lock()
	u := h.byKey[key]
	h.mu.Unlock()
	if u == nil {
		hashPw(pw, dummySalt)
		return nil, "", herr(401, "Email ya password galat hai.")
	}
	if !checkPw(u, pw) {
		return nil, "", herr(401, "Email ya password galat hai.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if u.Blocked {
		return nil, "", herr(403, "Aapka account block hai. Owner se contact karo (Contact page par bina login ke bhi message ja sakta hai).")
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
		return herr(429, "Bahut zyada koshish. Thodi der baad aao.")
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
		return herr(400, "OTP galat hai ya purana ho gaya. Dobara \"Forgot password\" karo.")
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
		return herr(401, "Pehle Sign In karo.")
	}
	if !h.rl.allow("echg:"+uid, 8, time.Hour) {
		return herr(429, "Bahut zyada koshish. Thodi der baad aao.")
	}
	if !checkPw(u, pw) {
		return herr(401, "Password galat hai.")
	}
	h.mu.Lock()
	if key == u.Key && disp == u.Email {
		h.mu.Unlock()
		return herr(400, "Ye wahi email hai jo abhi laga hai.")
	}
	if o := h.byKey[key]; o != nil && o.ID != uid {
		h.mu.Unlock()
		return herr(409, "Ye Gmail kisi aur account se juda hua hai.")
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
		return "", herr(400, "Pehle naya email daalo.")
	}
	if err := verify(&e.OTP, "echg:"+uid+":"+e.Key, code, time.Now()); err != nil {
		return "", err
	}
	if o := h.byKey[e.Key]; o != nil && o.ID != uid {
		return "", herr(409, "Ye Gmail kisi aur account se juda hua hai.")
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
		return 0, herr(400, "Data galat ya bahut bada hai.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return 0, herr(401, "Pehle Sign In karo.")
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
		return nil, herr(400, "Type chuno: Movie / Series / Anime / Cartoon.")
	}
	name = oneLine(name, 100)
	if utf8.RuneCountInString(name) < 2 {
		return nil, herr(400, "Movie / series ka naam likho.")
	}
	if year != 0 && (year < 1900 || year > time.Now().Year()+2) {
		return nil, herr(400, "Saal sahi daalo (ya khaali chhodo).")
	}
	note = cleanText(note, 200)
	if !h.rl.allow("req:"+who, 6, time.Hour) || !h.rl.allow("reqip:"+ip, 15, time.Hour) {
		return nil, herr(429, "Ek ghante mein itni requests nahi. Thodi der baad aao.")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	nn := normText(name)
	for _, r := range h.requests {
		if r.Who == who && r.Status != "rejected" && normText(r.Name) == nn && (r.Status == "pending" || r.Status == "working") {
			return nil, herr(409, "Ye request pehle se bheji hui hai.")
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
	"added":    "✅ Aapki request “%s” site par add ho gayi hai! Search karke dekh lo.",
	"working":  "⏳ “%s” par kaam chal raha hai, jaldi aayegi.",
	"rejected": "❌ “%s” abhi available nahi ho paayi. Baad mein dobara try kar sakte ho.",
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
		return nil, herr(400, "Message khaali hai.")
	}
	name = oneLine(name, 40)
	if name == "" {
		name = "Guest"
	}
	if !h.rl.allow("msg:"+who, 8, time.Hour) || !h.rl.allow("msgip:"+ip, 20, time.Hour) {
		return nil, herr(429, "Ek ghante mein itne message nahi. Thodi der baad aao.")
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
	u := h.users[id]
	if u == nil {
		return false
	}
	u.Blocked = b
	if b {
		h.dropSessions(id)
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
