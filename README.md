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
| `GMAIL_USER` | 🔐 login ke liye | tumhara Gmail, jaise `rajdev@gmail.com` |
| `GMAIL_APP_PASSWORD` | 🔐 login ke liye | Google **App Password** (16 akshar), asli Gmail password nahi. Neeche steps dekho |
| `OWNER_NAME` | optional | Owner page par naam (default `Raj Dev`) |
| `OWNER_ABOUT` | optional | Owner page ka "about me" text |
| `OWNER_PHOTO_URL` | optional | Owner ki photo ka seedha link |
| `OWNER_TELEGRAM` | optional | Telegram username (bina @), "Telegram par milo" button ke liye |
| `ALLOWED_EMAIL_DOMAINS` | optional | default `gmail.com,googlemail.com`. Sab allow karna ho toh `*` |
| `DB_NAME` | optional | default `astratoonix_nx` |
| `SITE_NAME` | optional | default `AstraToonix` |
| `LOGO_URL` | optional | logo ki image ka seedha link (navbar + player) |
| `DEFAULT_THUMB_URL` | optional | jin titles ka poster nahi hai unke liye default image |
| `PORT` | optional | default `8080` |
| `STREAM_CONCURRENCY`, `STREAM_BUFFER_COUNT` | optional | streaming speed tuning (default 4 aur 8) |

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
| `/users` | saare users, har ek ke saath 🚫 Block / ✅ Unblock / 🗑 Delete |
| `/user <email ya id>` | ek user ki detail |
| `/block`, `/unblock`, `/deluser <email ya id>` | seedha email se |
| `/setemail <email ya id> <naya email>` | user ne galat email se account banaya aur purane tak pahunch nahi, tab |
| `/requests` | pending movie/series requests (✅ ⏳ ❌ buttons) |
| `/broadcast <text>` | site par update, 🔔 mein dikhta hai |
| `/updates` | purane updates, 🗑 se hatao |
| *(user ke message ko Reply)* | jawab site par user ko dikhta hai |

`<id>` = bot ke message mein likha 🆔 number.

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
