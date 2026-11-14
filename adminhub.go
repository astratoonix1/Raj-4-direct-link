package main

// adminhub.go — Telegram admin side: requests, user messages, replies, users, broadcast.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var reThread = regexp.MustCompile(`🧵 (T[0-9a-f]{8})`)

func (a *App) toAdmins(text string, kb *tgbotapi.InlineKeyboardMarkup) {
	text = strings.ToValidUTF8(text, "")
	if r := []rune(text); len(r) > 3800 {
		text = string(r[:3800]) + "..."
	}
	for id := range a.cfg.Admins {
		msg := tgbotapi.NewMessage(id, text)
		if kb != nil {
			msg.ReplyMarkup = *kb
		}
		if _, err := a.bot.Send(msg); err != nil {
			a.logger.Warn("admin notify failed")
		}
	}
}

func reqKeyboard(id string) *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("✅ Add ho gayi", "rq:"+id+":a"),
		tgbotapi.NewInlineKeyboardButtonData("⏳ Kaam chal raha", "rq:"+id+":w"),
		tgbotapi.NewInlineKeyboardButtonData("❌ Nahi mili", "rq:"+id+":r"),
	))
	return &kb
}

func reqText(r *Request) string {
	icon := map[string]string{"pending": "🆕", "working": "⏳", "added": "✅", "rejected": "❌"}[r.Status]
	t := fmt.Sprintf("%s Request #%s — %s\n🎬 %s", icon, r.ID, r.Kind, r.Name)
	if r.Year > 0 {
		t += fmt.Sprintf(" (%d)", r.Year)
	}
	if r.Note != "" {
		t += "\n📝 " + r.Note
	}
	who := r.UserName
	if r.Email != "" {
		who += " (" + r.Email + ")"
	}
	t += "\n👤 " + who + "\n📌 Status: " + r.Status + "\n🧵 " + ThreadCode(r.Who)
	return t
}

func (a *App) notifyRequest(r *Request) {
	a.toAdmins(reqText(r)+"\n\n↩️ Is message ko Reply karo toh user ko site par jawab dikhega.", reqKeyboard(r.ID))
}

func (a *App) notifyUserMsg(m *Msg, code, email string) {
	who := m.Name
	if email != "" {
		who += " (" + email + ")"
	}
	a.toAdmins(fmt.Sprintf("📩 Naya message — %s\n\n%s\n\n🧵 %s\n↩️ Reply karo, jawab site par dikhega.", who, m.Text, code), nil)
}

// adminReply: admin ne bot ke kisi 🧵 wale message ko reply kiya.
func (a *App) adminReply(m *tgbotapi.Message) bool {
	rm := m.ReplyToMessage
	if rm == nil {
		return false
	}
	g := reThread.FindStringSubmatch(rm.Text + " " + rm.Caption)
	if g == nil {
		return false
	}
	if strings.TrimSpace(m.Text) == "" {
		a.send(m.Chat.ID, "Reply sirf text mein chalega.")
		return true
	}
	if _, err := a.hub.AdminReply(g[1], m.Text); err != nil {
		a.send(m.Chat.ID, "❌ "+err.Error())
		return true
	}
	a.send(m.Chat.ID, "✅ Jawab site par bhej diya.")
	return true
}

// ---------- premium ----------

func payKeyboard(id string) *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("✅ Approve", "pp:"+id+":ok"),
		tgbotapi.NewInlineKeyboardButtonData("❌ Reject", "pp:"+id+":no"),
	))
	return &kb
}

func payText(p *PayReq) string {
	st := map[string]string{"pending": "⏳ check karna baaki", "approved": "✅ Approved", "rejected": "❌ Rejected"}[p.Status]
	return fmt.Sprintf("💳 Premium payment #%s\n👤 %s (%s)\n📦 %d mahine — ₹%d\n🔢 UTR: %s\n🕒 %s\nStatus: %s",
		p.ID, p.Name, p.Email, p.Months, p.Amount, p.UTR, p.Created.Format("02 Jan 15:04"), st)
}

func (a *App) notifyPayment(p *PayReq) {
	a.toAdmins(payText(p)+"\n\n⚠️ Pehle apne bank / UPI app mein dekho ki ₹"+fmt.Sprint(p.Amount)+" is UTR se aaye hain, tabhi Approve dabao.", payKeyboard(p.ID))
}

func (a *App) premiumCommand(m *tgbotapi.Message, arg string) bool {
	chat := m.Chat.ID
	findUser := func(q string) *User {
		f := strings.Fields(q)
		if len(f) == 0 {
			return nil
		}
		return a.hub.FindUser(f[0])
	}
	switch m.Command() {
	case "code":
		f := strings.Fields(arg)
		months, n := 0, 1
		if len(f) > 0 {
			months, _ = strconv.Atoi(f[0])
		}
		if len(f) > 1 {
			n, _ = strconv.Atoi(f[1])
		}
		if months == 0 {
			a.send(chat, "Aise likho: /code <mahine> [kitne code]\nJaise: /code 1 5")
			return true
		}
		codes, err := a.hub.GenCodes(months, n)
		if err != nil {
			a.send(chat, "❌ "+err.Error())
			return true
		}
		a.send(chat, fmt.Sprintf("🎟 %d code (%d mahine ka har ek, ek hi baar chalega):\n\n%s", len(codes), months, strings.Join(codes, "\n")))
	case "codes":
		cs := a.hub.UnusedCodes()
		if len(cs) == 0 {
			a.send(chat, "Koi unused code nahi hai. /code 1 5 se banao.")
			return true
		}
		var b strings.Builder
		fmt.Fprintf(&b, "🎟 Unused codes: %d\n\n", len(cs))
		for _, c := range cs {
			fmt.Fprintf(&b, "%s — %d mahine\n", c.Code, c.Months)
		}
		a.send(chat, b.String())
	case "subs":
		us := a.hub.ActiveSubs()
		if len(us) == 0 {
			a.send(chat, "Abhi koi premium subscriber nahi hai.")
			return true
		}
		var b strings.Builder
		fmt.Fprintf(&b, "👑 Premium subscribers: %d\n\n", len(us))
		for i, u := range us {
			fmt.Fprintf(&b, "%d. %s — %s\n   %s tak\n", i+1, u.Name, u.Email, u.PremiumUntil.Format("02 Jan 2006"))
		}
		a.send(chat, b.String())
	case "grant":
		f := strings.Fields(arg)
		if len(f) != 2 {
			a.send(chat, "Aise likho: /grant <email ya id> <mahine>")
			return true
		}
		u := findUser(f[0])
		months, _ := strconv.Atoi(f[1])
		if u == nil {
			a.send(chat, "❌ Aisa user nahi mila.")
			return true
		}
		until, err := a.hub.Grant(u.ID, months)
		if err != nil {
			a.send(chat, "❌ "+err.Error())
			return true
		}
		a.send(chat, fmt.Sprintf("👑 %s ko premium mila, %s tak.", u.Email, until.Format("02 Jan 2006")))
	case "revoke":
		u := findUser(arg)
		if u == nil {
			a.send(chat, "Aise likho: /revoke <email ya id>")
			return true
		}
		a.hub.Revoke(u.ID)
		a.send(chat, "Premium hata diya: "+u.Email)
	case "payments":
		ps := a.hub.PendingPayments()
		if len(ps) == 0 {
			a.send(chat, "Koi pending payment nahi hai 🎉")
			return true
		}
		if len(ps) > 15 {
			ps = ps[:15]
		}
		for _, p := range ps {
			a.sendKB(chat, payText(p), payKeyboard(p.ID))
		}
	case "premium":
		f := strings.Fields(arg)
		if len(f) == 0 {
			from := a.cat.PremiumFrom()
			rule := "saal ka rule band (env PREMIUM_FROM_YEAR nahi hai)"
			if from > 0 {
				rule = fmt.Sprintf("%d aur uske baad ke saare titles premium", from)
			}
			a.send(chat, "👑 Premium rule: "+rule+"\n\nEk title ke liye: /premium <file id> on | off | auto\non = hamesha premium, off = hamesha free, auto = saal ka rule.\n/premiumlist = abhi premium titles.")
			return true
		}
		id, err := strconv.Atoi(f[0])
		if err != nil || len(f) < 2 {
			a.send(chat, "Aise likho: /premium <file id> on|off|auto")
			return true
		}
		access := map[string]string{"on": "premium", "off": "free", "auto": ""}
		ac, ok := access[strings.ToLower(f[1])]
		if !ok {
			a.send(chat, "on, off ya auto likho.")
			return true
		}
		title, ok := a.cat.SetAccess(context.Background(), id, ac)
		if !ok {
			a.send(chat, "❌ Ye file id nahi mila.")
			return true
		}
		a.send(chat, map[string]string{"on": "👑 Premium kiya: ", "off": "🆓 Free kiya: ", "auto": "↩️ Auto rule par: "}[strings.ToLower(f[1])]+title)
	case "premiumlist":
		ts := a.cat.PremiumTitles()
		if len(ts) == 0 {
			a.send(chat, "Abhi koi premium title nahi hai.")
			return true
		}
		a.send(chat, fmt.Sprintf("👑 Premium titles (%d):\n\n%s", len(ts), strings.Join(ts, "\n")))
	default:
		return false
	}
	return true
}

// ---------- users list ----------

const usersPerPage = 6

func (a *App) renderUsers(page int) (string, *tgbotapi.InlineKeyboardMarkup) {
	us := a.hub.ListUsers()
	total, blocked := a.hub.UserCount()
	if len(us) == 0 {
		return "Abhi koi user nahi hai.", nil
	}
	pages := (len(us) + usersPerPage - 1) / usersPerPage
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	chunk := us[page*usersPerPage:]
	if len(chunk) > usersPerPage {
		chunk = chunk[:usersPerPage]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "👥 Users: %d (🚫 blocked %d) — page %d/%d\n\n", total, blocked, page+1, pages)
	var rows [][]tgbotapi.InlineKeyboardButton
	for i, u := range chunk {
		n := page*usersPerPage + i + 1
		flag := ""
		if u.Blocked {
			flag = " 🚫"
		}
		fmt.Fprintf(&b, "%d. %s%s\n   %s · id %s · %s\n", n, u.Name, flag, u.Email, u.ID, u.Created.Format("02 Jan 06"))
		tog := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🚫 Block %d", n), fmt.Sprintf("ub:%s:%d", u.ID, page))
		if u.Blocked {
			tog = tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("✅ Unblock %d", n), fmt.Sprintf("uu:%s:%d", u.ID, page))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tog,
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🗑 Delete %d", n), fmt.Sprintf("ud:%s:%d", u.ID, page))))
	}
	var nav []tgbotapi.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️ Peeche", fmt.Sprintf("up:%d", page-1)))
	}
	if page < pages-1 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("Aage ▶️", fmt.Sprintf("up:%d", page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.String(), &kb
}

func userInfo(u *User) string {
	st := "✅ active"
	if u.Blocked {
		st = "🚫 blocked"
	}
	last := "—"
	if !u.LastLogin.IsZero() {
		last = u.LastLogin.Format("02 Jan 06 15:04")
	}
	return fmt.Sprintf("👤 %s\n📧 %s\n🆔 %s\nStatus: %s\nJoined: %s\nLast login: %s",
		u.Name, u.Email, u.ID, st, u.Created.Format("02 Jan 06"), last)
}

func (a *App) renderUpdates() (string, *tgbotapi.InlineKeyboardMarkup) {
	ups := a.hub.Updates(10)
	if len(ups) == 0 {
		return "Koi update nahi hai. /broadcast <text> se bhejo.", nil
	}
	var b strings.Builder
	var rows [][]tgbotapi.InlineKeyboardButton
	b.WriteString("📢 Updates (site par dikh rahe):\n\n")
	for i, u := range ups {
		t := []rune(u.Text)
		s := string(t)
		if len(t) > 90 {
			s = string(t[:90]) + "…"
		}
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, u.T.Format("02 Jan 15:04"), s)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🗑 Delete %d", i+1), "xd:"+u.ID)))
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.String(), &kb
}

func (a *App) sendKB(chat int64, txt string, kb *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chat, strings.ToValidUTF8(txt, ""))
	if kb != nil {
		msg.ReplyMarkup = *kb
	}
	a.bot.Send(msg)
}

// ---------- commands ----------

func (a *App) hubCommand(m *tgbotapi.Message) bool {
	chat := m.Chat.ID
	arg := strings.TrimSpace(m.CommandArguments())
	need := func(what string) (*User, bool) {
		if arg == "" {
			a.send(chat, "Aise likho: /"+m.Command()+" "+what)
			return nil, false
		}
		first := strings.Fields(arg)[0]
		u := a.hub.FindUser(first)
		if u == nil {
			a.send(chat, "❌ Aisa user nahi mila.")
			return nil, false
		}
		return u, true
	}
	if a.premiumCommand(m, arg) {
		return true
	}
	switch m.Command() {
	case "testmail":
		if arg == "" {
			a.send(chat, "Aise likho: /testmail tumhara@gmail.com")
			return true
		}
		if a.hub.mail == nil || !a.hub.mail.Enabled() {
			a.send(chat, "❌ GMAIL_USER / GMAIL_APP_PASSWORD abhi server ko mile nahi. Koyeb env check karo aur Redeploy karo.")
			return true
		}
		to := strings.Fields(arg)[0]
		a.send(chat, "📧 Test mail bhej raha hoon, 30 second tak ruko…")
		go func() {
			err := a.hub.mail.Send(to, "AstraToonix test mail", "Ye ek test mail hai. Ye mil gayi toh OTP bhi aayega.", "<p>Ye ek test mail hai. Ye mil gayi toh OTP bhi aayega.</p>")
			if err != nil {
				a.send(chat, "❌ Mail fail hui. Wajah:\n"+err.Error())
				return
			}
			a.send(chat, "✅ Gmail ne mail le li. "+to+" ka Inbox / Spam dekho. Bhejne wale Gmail ke Sent folder mein bhi dikhni chahiye.")
		}()
	case "clearbell":
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🧹 Sirf auto status (\"add ho gayi\" wale)", "cb:auto")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🗑 Sab 🔔 messages (updates + jawab + status)", "cb:all")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("↩️ Rehne do", "cb:no")))
		a.sendKB(chat, "🔔 Bell ke messages hatane hain?\n\nUsers, requests aur movies/files nahi chhute jaate. User ke apne bheje messages bhi rehte hain.", &kb)
	case "users":
		txt, kb := a.renderUsers(0)
		a.sendKB(chat, txt, kb)
	case "user":
		if u, ok := need("<email ya id>"); ok {
			a.send(chat, userInfo(u))
		}
	case "block", "unblock":
		if u, ok := need("<email ya id>"); ok {
			b := m.Command() == "block"
			a.hub.SetBlocked(u.ID, b)
			if b {
				a.send(chat, "🚫 Block kiya: "+u.Email)
			} else {
				a.send(chat, "✅ Unblock kiya: "+u.Email)
			}
		}
	case "deluser":
		if u, ok := need("<email ya id>"); ok {
			a.hub.DeleteUser(u.ID)
			a.send(chat, "🗑 User delete: "+u.Email+" (ab ye email naya account bana sakta hai)")
		}
	case "setemail":
		f := strings.Fields(arg)
		if len(f) != 2 {
			a.send(chat, "Aise likho: /setemail <purana email ya id> <naya email>")
			return true
		}
		u := a.hub.FindUser(f[0])
		if u == nil {
			a.send(chat, "❌ Aisa user nahi mila.")
			return true
		}
		if err := a.hub.AdminSetEmail(u.ID, f[1]); err != nil {
			a.send(chat, "❌ "+err.Error())
			return true
		}
		a.send(chat, "✅ Email badal diya: "+f[1])
	case "requests":
		rs := a.hub.PendingRequests()
		if len(rs) == 0 {
			a.send(chat, "Koi pending request nahi hai 🎉")
			return true
		}
		if len(rs) > 15 {
			rs = rs[:15]
		}
		for _, r := range rs {
			a.sendKB(chat, reqText(r), reqKeyboard(r.ID))
		}
	case "broadcast":
		if arg == "" {
			a.send(chat, "Aise likho: /broadcast Naya season aa gaya!")
			return true
		}
		u, err := a.hub.AddUpdate(arg)
		if err != nil {
			a.send(chat, "❌ "+err.Error())
			return true
		}
		a.send(chat, "📢 Site par update lag gaya (id "+u.ID+"). Sabko 🔔 mein dikhega. Hatane ke liye /updates.")
	case "updates":
		txt, kb := a.renderUpdates()
		a.sendKB(chat, txt, kb)
	default:
		return false
	}
	return true
}

// ---------- callbacks ----------

func (a *App) hubCallback(cq *tgbotapi.CallbackQuery) bool {
	p := strings.Split(cq.Data, ":")
	switch p[0] {
	case "rq", "ub", "uu", "ud", "udc", "up", "xd", "cb", "pp":
	default:
		return false
	}
	if cq.From == nil || !a.cfg.Admins[cq.From.ID] {
		a.bot.Request(tgbotapi.NewCallback(cq.ID, "Sirf admin"))
		return true
	}
	ans := func(t string) { a.bot.Request(tgbotapi.NewCallback(cq.ID, t)) }
	var chat int64
	var mid int
	if cq.Message != nil {
		chat, mid = cq.Message.Chat.ID, cq.Message.MessageID
	}
	edit := func(txt string, kb *tgbotapi.InlineKeyboardMarkup) {
		if mid == 0 {
			return
		}
		var em tgbotapi.EditMessageTextConfig
		if kb != nil {
			em = tgbotapi.NewEditMessageTextAndMarkup(chat, mid, strings.ToValidUTF8(txt, ""), *kb)
		} else {
			em = tgbotapi.NewEditMessageText(chat, mid, strings.ToValidUTF8(txt, ""))
		}
		a.bot.Send(em)
	}
	page := func(i int) int { n, _ := strconv.Atoi(p[i]); return n }
	switch p[0] {
	case "rq":
		if len(p) < 3 {
			return true
		}
		st := map[string]string{"a": "added", "w": "working", "r": "rejected"}[p[2]]
		r, ok := a.hub.SetRequestStatus(p[1], st)
		if !ok {
			ans("Request nahi mili")
			return true
		}
		ans("Status: " + st + " — user ko site par dikh jayega")
		edit(reqText(r), reqKeyboard(r.ID))
	case "up":
		if len(p) > 1 {
			txt, kb := a.renderUsers(page(1))
			edit(txt, kb)
		}
		ans("")
	case "ub", "uu":
		if len(p) < 3 {
			return true
		}
		a.hub.SetBlocked(p[1], p[0] == "ub")
		if p[0] == "ub" {
			ans("🚫 Block ho gaya")
		} else {
			ans("✅ Unblock ho gaya")
		}
		txt, kb := a.renderUsers(page(2))
		edit(txt, kb)
	case "ud":
		if len(p) < 3 {
			return true
		}
		u := a.hub.UserByID(p[1])
		if u == nil {
			ans("User nahi mila")
			return true
		}
		ans("")
		kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Haan, delete", "udc:"+u.ID+":"+p[2]),
			tgbotapi.NewInlineKeyboardButtonData("↩️ Nahi", "up:"+p[2])))
		edit("Pakka delete karna hai?\n\n"+userInfo(u)+"\n\nUska My List / history bhi hat jayega aur ye email naya account bana sakega.", &kb)
	case "udc":
		if len(p) < 3 {
			return true
		}
		a.hub.DeleteUser(p[1])
		ans("🗑 Delete ho gaya")
		txt, kb := a.renderUsers(page(2))
		edit(txt, kb)
	case "pp":
		if len(p) < 3 {
			return true
		}
		if p[2] == "ok" {
			pr, until, err := a.hub.ApprovePayment(p[1])
			if err != nil {
				ans(err.Error())
				if pr != nil {
					edit(payText(pr), nil)
				}
				return true
			}
			ans("👑 Premium chalu, " + until.Format("02 Jan 2006") + " tak")
			edit(payText(pr)+"\nTak: "+until.Format("02 Jan 2006"), nil)
		} else {
			pr, err := a.hub.RejectPayment(p[1])
			if err != nil {
				ans(err.Error())
				if pr != nil {
					edit(payText(pr), nil)
				}
				return true
			}
			ans("❌ Reject ho gaya")
			edit(payText(pr), nil)
		}
	case "cb":
		if len(p) < 2 || p[1] == "no" {
			ans("Theek hai")
			edit("Rehne diya, kuch nahi hata.", nil)
			return true
		}
		u, m := a.hub.ClearBell(p[1])
		ans("🗑 Ho gaya")
		edit(fmt.Sprintf("✅ Bell saaf: %d updates aur %d messages hata diye.", u, m), nil)
	case "xd":
		if len(p) > 1 {
			a.hub.DeleteUpdate(p[1])
		}
		ans("🗑 Hata diya")
		txt, kb := a.renderUpdates()
		edit(txt, kb)
	}
	return true
}
