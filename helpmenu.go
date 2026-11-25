package main

// Admin bot ka /start aur /help: pehle sirf buttons, button dabane par us hisse ke commands.

import (
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type helpSec struct{ key, label, text string }

var helpSecs = []helpSec{
	{"files", "🎬 Titles & Files", `📤 UPLOAD
Koi video ya file bhejo → main naam padhkar TMDB se title dhoondhta hoon, sahi category mein daalta hoon aur link deta hoon. Kai files ek saath bhejo toh ek hi message aata hai.

🎬 TITLES & FILES
/list — saare titles, har ek ke saath 🗑 delete
/others — jinka TMDB match nahi mila (5-5 karke)
/delothers [n] — sabse naye n Other files hatao (default 5), /delothers all = sab Other
/fix <id> <tmdb link> — galat title theek karo
   jaise: /fix 123 https://www.themoviedb.org/movie/603
/cat <id> movie|series|anime|cartoon|other — category badlo
/thumb <id> <image url> — apna poster lagao (/thumb <id> off = hatao)
/retry — Other ki files dobara TMDB se milao
/del <id> — ek file hatao
/deltitle <id> — us title ki saari files hatao
/deleteall — SAB kuch hatao (confirm ke baad)
/link <id> — link dobara
/stats — kitna kya hai
/tmdb — TMDB connect hai ya nahi
/id — apna Telegram ID

(<id> = bot ke message mein likha 🆔 number)

⬇️ Users, premium, ad, updates ki list neeche dusre message mein hai.

(<id> = bot ke message mein likha 🆔 number)`},
	{"users", "👥 Users", `👥 USERS
/users — saare users (🚫 block, ✅ unblock, 🗑 delete)
/user <email ya id> — ek user ki detail
/visitors — abhi kitne log site par, aaj, 7 din, guest vs account
/guests — bina login wale devices (kab aaye) + 🚫 Block / 💬 Message button
/blocked — block hue users + devices: kab block hua, kab khulega, ✅ Unblock, 💬 Message
Time ke saath block: /block <email/NX ID> 7d, /blockdev <code> 12h (30m, 12h, 7d, 2w). Time nahi diya toh hamesha.
/blockdev <device code> — us device ko block (site par kuch nahi dikhega), /unblockdev <code>
Har user ka ID NX123456 jaisa hota hai: /user, /block, /deluser mein ye ID ya email dono chalte hain.
/block /unblock /deluser <email ya id> (ya 🧵 message ko Reply karke sirf /block)
/setemail <email ya id> <naya email> — galat email theek karo`},
	{"msgs", "💬 Messages", `💬 MESSAGES & UPDATES
/requests — pending movie/series requests (✅ ⏳ ❌)
/broadcast <text> — site par sabko update (🔔)
/updates — purane updates (🗑 se hatao)
/clearbell — 🔔 ke messages saaf: auto status / sab bell / poori chat (user ke messages samet)
User ke message ko Reply karo → jawab site par dikhta hai.`},
	{"prem", "👑 Premium", `👑 PREMIUM
/premiumon, /premiumoff — poora premium system chalu / band (turant)
/premium — abhi ka haal + rule
/trial — free trial ka haal (/trial on, /trial off, /trial <din>) — har account/device ko ek baar
/premium <file id> on|off|auto — ek title hamesha premium / hamesha free / saal ke rule par
/premiumlist — premium titles ki list
/code <mahine> [kitne] — redeem code banao, jaise /code 1 5
/codes — unused codes, /subs — premium users
/grant <email ya id> <mahine> — seedha premium do
/revoke <email ya id> — premium hatao
/payments — pending payments (✅ Approve / ❌ Reject)`},
	{"vip", "🔵 VIP & Tick", `🔵 VIP & BLUE TICK
/vip <email ya NX ID> — hamesha ka VIP (720p/1080p/4K + no ads + 🔵 tick), /unvip
/verify <email ya NX ID> — sirf 🔵 Blue Tick, /unverify
/vips — VIP aur Blue Tick wale sab
(Site admin accounts ke liye env ADMIN_EMAILS)`},
	{"ads", "📺 Ads", `📺 ADSTERRA ADS (VIP/admin ko nahi dikhte)
/ads — haal
/adsbanner <code> — banner code, /adspop <code> — popunder/social bar code
/adson, /adsoff, /adsclear [banner|pop]`},
	{"ai", "🤖 AI", `🤖 AI (Gemini)
/ai — haal, /aion, /aioff — Contact Owner chat mein AI assistant
(Admin Agent chat: site ke ⋮ menu → 🤖 Agent, env GEMINI_API_KEY + ADMIN_EMAILS)`},
	{"idx", "🔎 Auto-Index", `🔎 AUTO-INDEX (channel se)
/idx — haal, /idxon, /idxoff, /idxscan <naam>, /idxsync — channel se dhundh kar index karo
/idxall — POORA channel andar se scan karke sab files index (status / stop / reset)
(env TG_STRING_SESSION; user search/request par file apne-aap site par aa jaati hai)`},
	{"qual", "🎞 Quality & Sub", `🎞 QUALITY & SUBTITLES
/qualitylock on|off — free users 480p tak, 720p+ sirf VIP (premium system ON ho tab)
/addsub <file id> <language> <https .srt/.vtt link>, /sublist <file id>, /subdel <file id> <number>`},
	{"pop", "📢 Ad Popup", `📢 AD POPUP (home page ka gol)
Photo bhejo, caption: /ad text | https://link
ya /ad <image https link> | text | link
/ad — haal, /adon /adoff /addel`},
	{"test", "🔧 Test", `🔧 TEST
/testmail <email> — Gmail OTP mail jaa rahi hai ya nahi`},
}

const helpHome = "👋 Admin panel — ye bot tumhari site ka remote control hai.\n\n" +
	"📤 Koi video ya file bhejo → main naam padhkar TMDB se title dhoondhta hoon, sahi category mein daalta hoon aur link deta hoon.\n\n" +
	"Neeche button dabao — aapko kis cheez ke liye command chahiye?"

func helpMenuMarkup() tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	var cur []tgbotapi.InlineKeyboardButton
	for _, s := range helpSecs {
		cur = append(cur, tgbotapi.NewInlineKeyboardButtonData(s.label, "hp:"+s.key))
		if len(cur) == 2 {
			rows = append(rows, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	rows = append(rows, []tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("📜 Sab commands ek saath", "hp:all")})
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// sendHelpMenu: /start, /help
func (a *App) sendHelpMenu(chat int64) {
	msg := tgbotapi.NewMessage(chat, helpHome)
	msg.ReplyMarkup = helpMenuMarkup()
	a.bot.Send(msg)
}

// helpCallback: "hp:<key>" button. true = ye callback yahin sambhal liya.
func (a *App) helpCallback(cq *tgbotapi.CallbackQuery) bool {
	if !strings.HasPrefix(cq.Data, "hp:") {
		return false
	}
	a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))
	if cq.Message == nil {
		return true
	}
	chat, mid := cq.Message.Chat.ID, cq.Message.MessageID
	key := strings.TrimPrefix(cq.Data, "hp:")
	if key == "all" {
		for _, s := range helpSecs {
			a.send(chat, s.label+"\n\n"+s.text)
		}
		return true
	}
	if key == "menu" {
		em := tgbotapi.NewEditMessageTextAndMarkup(chat, mid, helpHome, helpMenuMarkup())
		a.bot.Request(em)
		return true
	}
	for _, s := range helpSecs {
		if s.key == key {
			back := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Menu", "hp:menu")))
			em := tgbotapi.NewEditMessageTextAndMarkup(chat, mid, s.label+"\n\n"+s.text, back)
			a.bot.Request(em)
			return true
		}
	}
	return true
}
