# AstraToonix NX 🎬

Telegram se chalne wala, Netflix jaisa streaming site.
Bot ko video bhejo → bot file ke naam se **TMDB** par title dhoondhta hai → sahi category mein daal deta hai → site par poster, rating, cast, episodes aur parts ke saath dikhta hai.

> Sirf apni / public-domain / licensed content ke liye use karo.

---

## ✨ Kya-kya hai

- **Auto detect (TMDB):** file ke naam se Movie / Series / Anime / Cartoon pehchaanta hai. Match na mile toh **Other** mein jaata hai.
- **Netflix-style site:** hero banner, rows (New, Top 10, Movies, TV Shows, Anime, Cartoon, genres), hover card, drag scroll, Search, My List, Like, Continue Watching, Sign In (sirf us device par).
- **Player page:** resume, agla episode auto (8 sec), quality boxes (thumbnail + 1080p/720p/480p), Download, Link copy, aur neeche episodes / parts / suggestions.
- **Parts:** movie ka Part 2, Part 3 TMDB collection se dikhta hai (jo upload nahi hua wo dhundhla).
- **Kai quality = ek card:** 480p/720p/1080p ek hi title mein judti hain.
- **Bot se poora control:** ek saath aayi files ka ek hi message, 🗑 delete buttons, `/list`, `/others`, `/fix`, `/cat`, `/thumb` aur bhi.
- **Dekhne ke liye login zaroori nahi:** link kholne wala seedha dekh sakta hai. Upload sirf admin kar sakta hai.
- **⋮ menu:** 🎬 Movie/Series Request, 👤 Owner/About + message, 🔔 Updates.
- **Request:** naam likhte hi TMDB suggestions, site par pehle se ho toh bata deta hai. Tumhe Telegram par ✅ / ⏳ / ❌ buttons ke saath aati hai, status user ko site par dikhta hai.
- **Owner se baat:** user message bhejta hai → tumhe Telegram par aata hai → bot message ko **Reply** karo → jawab site par dikhta hai.
- **Broadcast:** `/broadcast text` → sabko 🔔 mein dikhta hai.
- **Account (Gmail + password + OTP):** ek Gmail = ek account. My List, Likes, Continue Watching har device par saath. Galat OTP se account block nahi hota (5 galat → naya OTP maango). Forgot password aur email badalna (naye email par OTP) bhi hai.
- **Premium (👑):** `PREMIUM_FROM_YEAR` ke baad ke titles lock. Plan chuno → QR se pay → UTR daalo → tumhe Telegram par Approve/Reject → user ka premium chalu. Redeem code bhi chalta hai. Premium click karte hi animation.
- **Admin control:** `/users` se block / unblock / delete.

---

## 📁 File structure

Sab files repo ke **root** mein rakho (koi subfolder nahi):

```
├── Dockerfile        Koyeb isi se build karta hai
├── go.mod            Go dependencies
├── main.go           bot, Telegram se video khinchna, server
├── tmdb.go           file ke naam se title pehchanna + TMDB
├── catalog.go        titles/files list, rows, parts, episodes
├── pipeline.go       upload hote hi: match → category → jodna
├── batch.go          ek saath aayi files ka ek message
├── api.go            site ke data endpoints
├── api2.go           login, OTP, request, message, updates endpoints
├── hub.go            users, OTP, sessions, requests, messages
├── mailer.go         Gmail se OTP email bhejna
├── adminhub.go       bot: /users, /broadcast, request buttons, reply
├── ad.go, adbot.go   home page ka gol ad popup
├── block.go          user / device block, visitors, guests
├── agent.go          Gemini AI: owner chat assistant + Admin Agent
├── vip.go            Adsterra ads, VIP + Blue Tick, quality lock, subtitles
├── notify.go         naya episode alert (My List wale users ko bell notice)
├── admin.go          admin dashboard data (/api/admin/stats)
├── pwa.go            PWA: manifest, service worker, icons
├── store_mongo.go    MongoDB mein save karna
├── index.html        home, detail, search, My List, login
├── watch.html        player page
└── README.md
```

`index.html` aur `watch.html` ka naam mat badalna (code unhe naam se andar daalta hai).
`go.sum` nahi chahiye, Dockerfile build ke time `go mod tidy` se bana leti hai.

---

## 🔑 Environment variables (Koyeb)

| Variable | Zaroori? | Kya daalna hai |
|---|---|---|
| `BOT_TOKEN` | ✅ | BotFather wala token |
| `API_ID`, `API_HASH` | ✅ | my.telegram.org se |
| `ADMIN_ID` | ✅ | tumhara Telegram ID (bot ko `/id` bhejo). Kai ho toh comma se: `111,222` |
| `CHANNEL_ID` | ✅ | storage channel, jaise `-1001234567890`. Bot us channel mein **admin** ho |
| `FQDN` | ✅ | `myapp.koyeb.app` (https:// ke bina) |
| `DB_URI` | ✅ | MongoDB ka link (Atlas free chalega) |
| `TMDB_API_KEY` | ⭐ | themoviedb.org → Settings → API (v3 key ya v4 token). Na ho toh sab **Other** mein jaayega |
| `GMAIL_USER` | 🔐 login ke liye | tumhara Gmail, jaise `rajdev9101@gmail.com` |
| `GMAIL_APP_PASSWORD` | 🔐 login ke liye | Google **App Password** (16 akshar), asli Gmail password nahi. Neeche steps dekho |
| `OWNER_NAME` | optional | Owner page par naam (default `Raj Dev`) |
| `OWNER_ABOUT` | optional | Owner page ka "about me" text |
| `OWNER_PHOTO_URL` | optional | Owner ki photo ka seedha link |
| `OWNER_TELEGRAM` | optional | Telegram username (bina @), "Telegram par milo" button ke liye |
| `PREMIUM` | optional | `on` daalo toh premium system pehli baar se chalu. Khaali = **band** (sab free). Baad mein bot se `/premiumon` / `/premiumoff` se turant badal sakte ho, redeploy nahi chahiye |
| `PREMIUM_FROM_YEAR` | optional | jaise `2015`: is saal aur uske baad ke saare titles **Premium** (naam ke saal se). Khaali = saal ka rule band |
| `PLAN_PRICE` | optional | 1 mahine ka rate, default `99`. Plans: 1, 2, 3, 6, 12 mahine (mahine x rate) |
| `PLANS` | optional | apne plan/discount: `1=99,2=180,6=500,12=900` (mahine=daam) |
| `PAY_QR_URL` | optional | payment QR image ka seedha link |
| `PAY_UPI_ID` | optional | UPI ID (site par copy button ke saath) |
| `WHATSAPP_URL` | optional | WhatsApp group ka invite link (`https://chat.whatsapp.com/...`). Home, title page aur login page par banner dikhta hai (English / हिंदी / বাংলা). Khaali ho toh banner nahi dikhta |
| `DEFAULT_QUALITY` | optional | `low` (default): Play dabane par sabse kam quality (480p) chalti hai, user chahe toh player mein badal sakta hai. `high` = sabse achhi quality pehle |
| `REQUIRE_LOGIN` | optional | `1` daalo toh **kisi bhi link par pehle Sign In / Sign Up** aayega, login ke baad wahi link khulega. Band karne ke liye env hata do. `GMAIL_*` set na ho toh ye apne aap off rehta hai |
| `ALLOWED_EMAIL_DOMAINS` | optional | default `gmail.com,googlemail.com`. Sab allow karna ho toh `*` |
| `DB_NAME` | optional | default `astratoonix_nx` |
| `SITE_NAME` | optional | default `AstraToonix` |
| `LOGO_URL` | optional | logo ki image ka seedha link (navbar + player) |
| `DEFAULT_THUMB_URL` | optional | jin titles ka poster nahi hai unke liye default image |
| `PORT` | optional | default `8080` |
| `GEMINI_API_KEY` | optional | Google Gemini API key (aistudio.google.com). Isse AI assistant + Admin Agent chalte hain |
| `GEMINI_MODEL` | optional | default `gemini-2.5-flash` |
| `ADMIN_EMAILS` | optional | site ke admin accounts (comma se alag): `me@gmail.com,two@gmail.com`. Inhe sab quality + no ads + 🔵 tick milta hai |
| `STREAM_CONCURRENCY` | optional | ek viewer ke liye ek saath kitne blocks Telegram se mangein (default `4`) — neeche "Streaming tuning" dekho |
| `STREAM_BUFFER_COUNT` | optional | ek viewer ke liye aage kitne blocks pehle se taiyaar rakhein (default `8`) |
| `STREAM_RPS` | optional | **saare viewers ke liye milakar** Telegram ko 1 second mein kitni requests (default `10`) |
| `STREAM_TIMEOUT_SEC`, `STREAM_MAX_RETRIES` | optional | ek block ka timeout aur retry (default `30` aur `3`) |

### 🔐 Gmail App Password kaise banaye
Asli Gmail password SMTP par kaam nahi karta, Google App Password maangta hai.
1. Google Account → **Security** → **2-Step Verification** ON karo.
2. Wahin search karo **App passwords** → naam likho (jaise `NX`) → **Create**.
3. 16 akshar ka password milega (`abcd efgh ijkl mnop`). Spaces ke saath ya bina, dono chalenge.
4. Koyeb mein `GMAIL_USER` aur `GMAIL_APP_PASSWORD` daalo, redeploy.
5. Env na daalo toh site normal chalegi, bas login/OTP band rahega.

OTP email kabhi **Spam** mein jaa sakta hai, user ko site par yahi likha dikhta hai. Gmail ki limit lagbhag 500 email/din hai.

---

## 🚀 Deploy (Koyeb)

1. Saari files GitHub repo ke root mein daalo.
2. Koyeb mein service banao, repo jodo.
3. **Builder = Dockerfile** chuno (Buildpack nahi, warna `go.sum` wali error aayegi).
4. Upar wale environment variables daalo.
5. Deploy karo. Logs mein ye lines aani chahiye:
   `catalog loaded` → `bot ready` → `HTTP server started` → `MTProto ready`
6. Bot ko `/tmdb` bhejo. "✅ TMDB connected successfully" aaye toh sab theek hai.

**Zaroori:** ek bot token par **ek hi instance** chalna chahiye. Do copy chalengi toh logs mein `Conflict: terminated by other getUpdates request` aayega.

---

## 🤖 Bot commands (sirf admin)

| Command | Kaam |
|---|---|
| *(video / file bhejo)* | upload + auto detect + link. Kai files ek saath bhejo toh **ek hi message** aata hai |
| `/list` | saare titles, har ek ke saath 📂 (files) aur 🗑 (delete) |
| `/others` | jinka TMDB match nahi mila, 5-5 karke. **Agle ▶️**, 🔄, har file ka 🗑 |
| `/delothers [n]` | sabse naye n Other files hatao (default 5). `/delothers all` = sab Other (confirm ke baad) |
| `/fix <id> <tmdb link>` | galat title theek karo (poori series saath shift hoti hai) |
| `/cat <id> movie\|series\|anime\|cartoon\|other` | category badlo |
| `/thumb <id> <image url>` | us title ka thumbnail apni image se (`/thumb <id> off` = hatao) |
| `/retry` | Other ki files dobara TMDB se milao |
| `/del <id>` | ek file hatao |
| `/deltitle <id>` | us title ki saari files (saari quality) hatao |
| `/deleteall` | **sab kuch** hatao (confirm ke baad) |
| `/link <id>` | link dobara |
| `/stats` | kitne titles/files, category ke hisaab se |
| `/tmdb` | TMDB connection test (The Matrix ki rating dikhata hai) |
| `/id` | apna Telegram ID |
| `/start`, `/help` | saare commands, kaise likhne hain us ke saath (do messages mein) |
| `/ad` | home page ka gol ad popup. Photo bhejo, caption: `/ad text \| https://link`. Ya `/ad <image https link> \| text \| link`. `/adon`, `/adoff`, `/addel` |
| `/premium` | premium ka abhi ka haal + rule |
| `/premiumlist` | premium titles ki list |
| `/code <mahine> [kitne]` | redeem code banao (ek code ek hi baar chalta hai). Jaise `/code 1 5` |
| `/codes`, `/subs` | unused codes, premium users |
| `/grant <email ya id> <mahine>`, `/revoke <email ya id>` | seedha premium do / hatao |
| `/payments` | pending payments, ✅ Approve / ❌ Reject buttons ke saath |
| `/premiumon`, `/premiumoff` | poora premium system chalu / band, turant. Band hone par sab free, Premium button site se hat jata hai, users/payments/codes safe rehte hain |
| `/premium <file id> on\|off\|auto` | ek title ko hamesha premium / hamesha free / saal ke rule par. `/premiumlist` se dekho |
| `/testmail <email>` | Gmail se mail jaa rahi hai ya nahi, test karo (galti hui toh wajah bhi batata hai) |
| `/users` | saare users, har ek ke saath 🚫 Block / ✅ Unblock / 🗑 Delete |
| `/user <email ya id>` | ek user ki detail |
| `/blockdev <device code>`, `/unblockdev <code>` | us device ko block: site par kuch nahi dikhega. Device code message ke neeche 📱 ke aage hota hai |
| `/visitors` | abhi online (5 min), aaj, 7 din, kul devices, guest vs account, signed-up users |
| `/guests` | bina login wale devices ki list (pehli/aakhri baar, visits), har ek par 🚫 Block / ✅ Unblock / 💬 Message |
| `/block <email/NX ID> [7d]`, `/blockdev <code> [12h]` | time ke saath block (30m, 12h, 7d, 2w); time nahi diya toh hamesha. Time poora hote hi apne aap unblock |
| `/blocked` | saare block kiye users ki list, har ek ke saath ✅ Unblock |
| `/block`, `/unblock`, `/deluser <email / NX ID>` (NX123456 wala ID bhi chalta hai) | seedha email se |
| `/setemail <email ya id> <naya email>` | user ne galat email se account banaya aur purane tak pahunch nahi, tab |
| `/requests` | pending movie/series requests (✅ ⏳ ❌ buttons) |
| `/broadcast <text>` | site par update, 🔔 mein dikhta hai |
| `/clearbell` | 🔔 ke saare messages ek saath hatao (confirm ke baad). Users, requests aur movies nahi chhute |
| `/updates` | purane updates, 🗑 se hatao |
| *(user ke message ko Reply)* | jawab site par user ko dikhta hai |

| `/vip <email/NX ID>`, `/unvip` | hamesha ka VIP (720p/1080p/4K + no ads + 🔵 tick) |
| `/verify <email/NX ID>`, `/unverify` | sirf 🔵 Blue Tick (VIP nahi) |
| `/ai`, `/aion`, `/aioff` | Contact Owner chat mein AI assistant ka haal / on / off |
| `/vips` | VIP aur Blue Tick wale sab |
| `/ads`, `/adson`, `/adsoff` | Adsterra ads ka haal / chalu / band |
| `/adsbanner <code>`, `/adspop <code>` | Adsterra banner code / popunder-social bar code paste karo. `/adsclear [banner\|pop]` se hatao |
| `/qualitylock on\|off` | ON: free = 480p tak, 720p+ sirf VIP (premium system ON ho tab kaam karta hai) |
| `/addsub <file id> <language> <https link>`, `/sublist <file id>`, `/subdel <file id> <number>` | video par subtitle (.srt/.vtt link) lagao / dekho / hatao |

`<id>` = bot ke message mein likha 🆔 number.

---

## 📺 Ads, VIP, Blue Tick, Player (naya)

- **Ads (Adsterra):** Adsterra se banner ka code lo → bot mein `/adsbanner <poora code>`; popunder / social bar ka code → `/adspop <code>`; phir `/adson`. Code **sirf guests aur free users ko** jata hai. Admin (`ADMIN_EMAILS`) aur VIP ko server code bhejta hi nahi, to unhe ad kabhi nahi dikhta. Banner ek sandbox iframe mein chalta hai (site ke data tak pahunch nahi); popunder script site ke page mein chalti hai, isliye sirf bharose ke network ka code lagao.
- **Status:** `free` / `vip` / `admin`. VIP = premium chalu. 🔵 Blue Tick VIP, admin aur `/verify` wale users ko milta hai: ⋮ menu, My Account aur Connect Owner chat mein.
- **Player:** purana wala (browser ka apna player: play/pause, seek, volume, fullscreen, speed, CC). Quality neeche ke boxes se badalti hai.
- **Quality lock:** `/premiumon` + `/qualitylock on` ke baad free users 480p tak; 720p/1080p/4K par "Go VIP" popup. Lock server par bhi laga hai (`/s/` aur `/d/` par 402), to link se bhi nahi khulta. Premium system OFF hai to lock apne aap band.
- **Subtitles:** `/addsub 1234 English https://…/en.srt` — server .srt ko .vtt mein badalta hai aur player ke CC menu mein dikhta hai.
- **TMDB link management** pehle jaisa hi hai (`/premium`, file ko title se jodna, `/tmdb`).

---

## 🎬 Streaming tuning (smooth video ke liye)

Pehle ye samjho: **in settings se video ki quality (480p/720p/1080p) nahi badalti** — wo file ki apni quality hoti hai. Ye settings sirf ye tay karti hain ki video **ruke bina** chale. Isliye 1080p file bhi bina atke chalane ke liye inhe badhate hain.

### Ye teen cheezein kya karti hain

| Setting | Default | Kaam |
|---|---|---|
| `STREAM_CONCURRENCY` | `4` | ek viewer ke liye ek saath 4 blocks (1 block = 1 MB) Telegram se kheenchte hain |
| `STREAM_BUFFER_COUNT` | `8` | ek viewer ke liye 8 MB aage ka video pehle se memory mein taiyaar rehta hai |
| `STREAM_RPS` | `10` | **sabhi viewers milakar** 1 second mein max 10 requests. Yahi asli speed ki chhat (ceiling) hai |

### Sabse zaroori baat
Telegram ko jaane wali requests ek **common limiter** se guzarti hain (`STREAM_RPS`). Har request ~1 MB laati hai, to default par poori site ki **kul speed ≈ 10 MB/s** hai, aur wo sab viewers mein baant jaati hai.

- 1 viewer, 1080p (≈ 1 MB/s chahiye) → bahut aaram se chalta hai.
- 5 viewer ek saath → har ek ko ≈ 2 MB/s → theek.
- 10+ viewer ek saath → har ek ko ≈ 1 MB/s se kam → **ruk-ruk ke chalega**.

Sirf `STREAM_CONCURRENCY` badhane se tab tak fayda nahi jab tak `STREAM_RPS` bhi na badhe, kyunki limiter pehle rok deta hai.

### Kitna rakhein (recommended)

| Plan | CONCURRENCY | BUFFER_COUNT | RPS | RAM / viewer (≈) | Kab |
|---|---|---|---|---|---|
| **Default (abhi)** | 4 | 8 | 10 | ~12 MB | 1–5 viewer, kaam chalau |
| **Better** | 6 | 12 | 20 | ~18 MB | 5–15 viewer, 1080p smooth |
| **Max** | 8 | 16 | 30 | ~24 MB | bahut viewer ya bhaari 1080p/4K files |

RAM ka hisaab: `(BUFFER_COUNT + CONCURRENCY) × 1 MB` har chalte stream par.

### Badhane ka fayda / nuksan

**Fayda**
- Video ke beech mein "buffering" kam hoti hai, aage seek karna tez hota hai.
- Zyada viewers ek saath bina atke dekh paate hain.

**Nuksan**
- Har viewer par zyada RAM lagti hai. Koyeb ki chhoti machine (512 MB) par 20 viewer × 24 MB = ~480 MB — crash ho sakta hai. Pehle **Better** try karo.
- `STREAM_RPS` bahut zyada rakha toh Telegram `FLOOD_WAIT` de sakta hai (kuch der ke liye rok deta hai) aur ulta atkega. 20–30 se upar mat jao.
- Viewer video beech mein chhod de toh pehle se kheenche blocks bekaar jaate hain (bandwidth waste).

### Purane repo (Raj stream) se tulna
Purane repo ki settings mujhe yahan dikh nahi rahi, isliye main uske number nahi bata sakta. Apne purane Koyeb project ke **Environment variables** mein `STREAM_CONCURRENCY`, `STREAM_BUFFER_COUNT` (ya jo bhi naam ho) dekh lo aur wahi numbers yahan daal do. Jo cheez main pakka bata sakta hoon: purane code mein Telegram connections ka pool tha aur request-limiter nahi tha, jabki ab **ek hi connection aur 10 requests/second ki limit** hai. Isliye wahi numbers (4/8) rakhne par bhi ab streaming kam smooth ho sakti hai — `STREAM_RPS` badhao (20 se shuru karo).

### Kaise badlein
Koyeb → Service → Settings → Environment variables mein daalo, phir Redeploy:
```
STREAM_CONCURRENCY=6
STREAM_BUFFER_COUNT=12
STREAM_RPS=20
```
Atakna band na ho toh Koyeb ke **Logs** mein `FLOOD_WAIT` dhundo. Mile toh RPS ghatao, na mile toh aur badha sakte ho.

---

## 🧠 Category kaise tay hoti hai

| Kya mila | Category |
|---|---|
| TMDB par movie | **Movie** |
| TMDB par series (`S01E02`, `1x02`, `Naam - 12` jaise naam) | **Series** |
| Animation + Japan | **Anime** |
| Animation + baaki desh | **Cartoon** |
| Match nahi mila | **Other** |

Galat lage toh `/fix` (title badlo) ya `/cat` (sirf category badlo).

---

## 📝 File ka naam kaisa rakho

Match utna sahi aayega jitna naam saaf hoga:

```
Inception.2010.1080p.mkv                 ✅ movie + year + quality
Breaking.Bad.S01E03.720p.mkv             ✅ series, season 1 episode 3
[SubsPlease] Frieren - 12 (1080p).mkv    ✅ anime episode
Harry Potter ... Part 2 2011 1080p.mkv   ✅ part
video123.mp4                             ❌ Other mein jaayega
```

- Quality naam se pehchani jaati hai: `480p`, `720p`, `1080p`, `4K`. Naam mein na ho toh "HD" dikhta hai.
- Ek hi title ki alag quality ke naam mein title same rakho, wo ek card mein judengi.

---

## 🗄️ Data kahan rehta hai

- **MongoDB:** sirf teen collections: `nx_titles`, `nx_files` aur `nx_docs` (users, requests, messages). Doosre collections ya doosre bot ke data ko ye nahi chhoota.
- **Telegram:** video storage channel mein rehti hai. `/del`, `/deltitle`, `/deleteall` aur 🗑 wahan se bhi wo message hatate hain jo NX ne khud forward kiya tha.
- **Browser:** My List, Likes, Continue Watching, resume position. Login ho toh My List/Likes/Continue Watching account mein bhi save hote hain.
- **Password:** kabhi seedha save nahi hota (PBKDF2 hash).

Auto filter jaise kisi doosre bot ke saath ho toh NX ke liye **alag storage channel** rakho.

---

## 🛠️ Dikkat aaye toh

| Dikkat | Kya karo |
|---|---|
| Sab file "Other" mein ja rahi | `TMDB_API_KEY` check karo, `/tmdb` chalao |
| Galat title laga | `/fix <id> <tmdb link>` |
| Bot ne "Storage channel mein nahi bhej paya" kaha | Bot ko channel mein admin banao, `CHANNEL_ID` `-100…` wala ho |
| "Ye bot sirf admin ke liye hai" | `ADMIN_ID` galat hai, `/id` se sahi number lo |
| Link khulne par "File nahi mili" | Wo file storage channel se hat chuki hai, dobara upload karo |
| Build mein `missing go.sum entry` | Koyeb mein Builder **Dockerfile** karo |
| `Conflict: terminated by other getUpdates` | Bot ki doosri copy band karo ya token revoke karke naya daalo |
| Login/OTP nahi chal raha | `GMAIL_USER` aur `GMAIL_APP_PASSWORD` (App Password) check karo. Logs mein `otp mail ... failed` aur wajah dikhegi |
| OTP email nahi aaya | Spam/Promotions dekho, 45 second baad "Naya OTP bhejo" |
| Home par kuch nahi dikhta | Pehli file upload karo; MongoDB `DB_URI` sahi hai ya nahi dekho |

---

Built by **Raj Dev** · Stream the Universe

---

## 🆕 Naya Episode Alert, 📊 Dashboard, 📲 PWA

**Naye episode ka alert.** Jis user ne series ko **My List** mein rakha hai, use us series ki nayi episode file judte hi bell icon (🔔 Updates) mein "🆕 <Title> ka naya episode aa gaya (S01E05)" notice milta hai, tap karke seedha title khulta hai. Ek saath 10 episode upload karo to **ek hi notice** jaata hai (10 minute jama karke: "10 naye episode, S01E01 se S01E10"). Sirf nayi file (6 ghante ke andar ki) par alert aata hai, `/fix` se purani file dobara attach karne par nahi. Movie par alert nahi aata. User notice ko "✖ Remove" se hata sakta hai, 20 notice aur 30 din se purane khud hat jaate hain.

**Admin Dashboard.** `ADMIN_EMAILS` wale account se login → ⋮ menu → **📊 Dashboard (Admin)** (`/#/admin`). Online abhi, aaj/7 din ke visitors, total aur naye users (14 din ka chart), premium/VIP, **kamai (aaj / 30 din / total)**, pending payments aur requests, **Unique devices** (kul kitne alag device aaye) aur **Unique viewers** (kitne alag log/device ne video chalayi, ek insaan ek hi baar), top 10 titles (👁 kul play · 👤 unique), latest signups, library size, system on/off. Har 30 second mein khud refresh hota hai. Data sirf admin ko milta hai (server check). Unique viewers ki ginti is update ke **baad** se shuru hoti hai (purane views mein ye info save nahi thi); login user ek insaan ki tarah ginta hai, guest device ke code se.

**PWA (App jaisa install).** Chrome/Android: ⋮ menu mein **📲 Install App (Home Screen)** dikhta hai, dabao aur icon home screen par aa jata hai, bina browser bar ke khulta hai. iPhone: Safari → Share → Add to Home Screen (button wahi bata deta hai). Icon aur manifest server khud banata hai, koi image upload nahi karni. Service worker sirf page-navigation sambhalta hai (internet na ho to "Internet nahi hai" page), API, video stream aur download ko nahi chhuta. Install ka button tabhi dikhta hai jab site **https** par ho (Koyeb par hoti hai).

## 🤖 AI (Gemini)

`GEMINI_API_KEY` daalo (aistudio.google.com se free key milti hai) aur redeploy karo.

**1. Contact Owner chat mein AI assistant.** User message bhejta hai to AI jawab deta hai (site ke plans, request kaise karein, premium kaise lein, uske apne request ka status…). Bot mein tumhe "🤖 AI ne jawab diya" ka note aata hai. Tum jab bhi Reply karo, us user ke thread mein AI **20 minute chup** rehta hai. Refund / payment problem / abuse jaise sawal par AI sirf "owner ko bata diya" bolta hai. AI koi action nahi kar sakta. `/aioff` se poora band.

**2. Admin Agent.** `ADMIN_EMAILS` wale account se login karo, ⋮ menu mein **🤖 Agent (Admin)**. Seedha likho: "site ka haal", "victim@gmail.com ko 7 din block karo", "ads band karo", "sab ko update bhejo: kal naya course aayega". Jaankari wale kaam turant hote hain. **Badlav wale kaam (block, VIP, ads, premium, broadcast, user ko message, payment approve…) pehle tumse Confirm / Cancel maangte hain.** User delete, email change, code ya env badalna agent nahi kar sakta.
Safety: agent sirf admin account ke liye hai (server check karta hai), users ke likhe text ko sirf data maana jata hai, aur confirm 15 minute mein expire ho jata hai.

**3. 🤖 Help Assistant (sab ke liye, guests bhi).** ⋮ menu mein **🤖 Help Assistant** aur Sign In / OTP page par "OTP nahi aaya? Help chahiye?" link. Ye sirf website use karna sikhata hai: Gmail se login/signup, OTP **spam / promotions folder** mein dhundna aur "Not spam" karna, Resend, Forgot password, Premium kaise lein (UPI + UTR), movie request, quality (480p free). Baaki sawaal (general gyaan, coding, chit-chat) ko polite "sirf website ki help" bolke mana kar deta hai. Ye koi action nahi kar sakta, koi tool nahi, koi admin data nahi. Limit: 20 sawaal/ghanta har user/guest, 40/ghanta har IP, sawaal max 500 akshar, sirf pichle 10 messages ka context. `/aioff` karoge to ye bhi band ho jata hai.

**Movie request AI se.** Koi Help Assistant ya Contact Owner chat mein bole "Raj ko bolo ye movie add kare" / "Inception add karo", to AI seedha **Request Movie** jaisi request bana deta hai (naam, type, saal). Tumhare bot mein wahi request notification aata hai jisme **ID, user ka naam/email aur title** hota hai, note mein "[AI se]" likha hota hai. User ko AI request ID batata hai aur status bell icon mein dikhta hai. Agar title site par pehle se hai ya user ne wahi request pehle bheji hai, to AI bata deta hai aur nayi request nahi banti. Limit wahi purani: 6 requests/ghanta har user, ek chat turn mein max 2. AI movie khud add nahi karta, sirf tumhe request bhejta hai.
