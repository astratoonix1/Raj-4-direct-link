package main

// tmdb.go — file ke naam se title pehchaanna + TMDB se jaankari laana.
// Is file mein sirf standard library hai.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var tmdbBase = "https://api.themoviedb.org/3"

const (
	imgPoster   = "https://image.tmdb.org/t/p/w342"
	imgBackdrop = "https://image.tmdb.org/t/p/w1280"
	imgProfile  = "https://image.tmdb.org/t/p/w185"
)

// ---------- file ke naam ko saaf karna ----------

var (
	reExt      = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|webm|m4v|ts|flv|mp3|m4a)$`)
	rePrefix   = regexp.MustCompile(`(?i)^(?:\s*\[[^\]]*\]|\s*\{[^}]*\}|\s*@\w+|\s*www\.[^\s]+\s*[-–]?)+`)
	reSep      = regexp.MustCompile(`[._\[\]{}()]+`)
	reYear     = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
	reNoise    = regexp.MustCompile(`(?i)\b(?:\d{3,4}p|4k|uhd|hdtc|hdcam|hdts|hdrip|camrip|cam|dvdscr|dvdrip|webrip|web-?dl|web|blu-?ray|brrip|bdrip|hdtv|hindi|english|tamil|telugu|malayalam|kannada|bengali|punjabi|japanese|korean|dual|multi|audio|dubbed|subbed|esubs?|subs?|x26[45]|h\.?26[45]|hevc|10bit|aac|ac3|ddp?\d?|atmos|yts|rarbg|extended|uncut|proper|remastered|complete|nf|amzn|zee5|hotstar|org)\b`)
	reSE1      = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*E(\d{1,3})\b`)
	reSE2      = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	reSE3      = regexp.MustCompile(`(?i)\bseason\s*(\d{1,2})\s*(?:episode|ep|e)\s*(\d{1,3})\b`)
	reSeasonOn = regexp.MustCompile(`(?i)\b(?:season|s)\s*(\d{1,2})\b`)
	reEp1      = regexp.MustCompile(`(?i)\b(?:episode|ep|e)\.?\s*(\d{1,3})\b`)
	reEpDash   = regexp.MustCompile(`\s-\s(\d{1,3})(?:v\d)?(?:\s|$)`)
	rePart     = regexp.MustCompile(`(?i)\b(?:part|chapter|vol(?:ume)?)\s*(\d{1,2})\b`)
	reNonWord  = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// ParsedName = file ke naam se nikli jaankari.
type ParsedName struct {
	Title    string
	Year     int
	Season   int
	Episode  int
	Part     int
	Quality  string
	QRank    int
	Episodic bool
}

func normText(s string) string {
	return strings.ToLower(reNonWord.ReplaceAllString(s, ""))
}

func detectQuality(name string) (string, int) {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "2160p") || strings.Contains(n, "4k") || strings.Contains(n, "uhd"):
		return "4K", 5
	case strings.Contains(n, "1080p"):
		return "1080p", 4
	case strings.Contains(n, "720p"):
		return "720p", 3
	case strings.Contains(n, "480p"):
		return "480p", 2
	case strings.Contains(n, "360p") || strings.Contains(n, "240p"):
		return "360p", 1
	case strings.Contains(n, "hdcam") || strings.Contains(n, "camrip") || strings.Contains(n, "hdts"):
		return "CAM", 0
	}
	return "HD", 2
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// parseName "Avengers.Endgame.2019.1080p.mkv" -> {Title:"Avengers Endgame", Year:2019, ...}
func parseName(name string) ParsedName {
	var pn ParsedName
	pn.Quality, pn.QRank = detectQuality(name)

	s := reExt.ReplaceAllString(strings.TrimSpace(name), "")
	s = rePrefix.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(reSep.ReplaceAllString(s, " ")), " ")

	// season / episode
	seIdx := -1
	setSE := func(idx int, season, ep int) {
		if seIdx == -1 || idx < seIdx {
			seIdx = idx
		}
		if pn.Season == 0 && season > 0 {
			pn.Season = season
		}
		if pn.Episode == 0 && ep > 0 {
			pn.Episode = ep
		}
		pn.Episodic = true
	}
	switch {
	case reSE1.MatchString(s):
		m := reSE1.FindStringSubmatchIndex(s)
		setSE(m[0], atoi(s[m[2]:m[3]]), atoi(s[m[4]:m[5]]))
	case reSE3.MatchString(s):
		m := reSE3.FindStringSubmatchIndex(s)
		setSE(m[0], atoi(s[m[2]:m[3]]), atoi(s[m[4]:m[5]]))
	case reSE2.MatchString(s):
		m := reSE2.FindStringSubmatchIndex(s)
		setSE(m[0], atoi(s[m[2]:m[3]]), atoi(s[m[4]:m[5]]))
	default:
		if m := reSeasonOn.FindStringSubmatchIndex(s); m != nil && m[0] > 0 {
			setSE(m[0], atoi(s[m[2]:m[3]]), 0)
		}
		if m := reEp1.FindStringSubmatchIndex(s); m != nil && m[0] > 0 {
			setSE(m[0], 0, atoi(s[m[2]:m[3]]))
		}
		if !pn.Episodic {
			if m := reEpDash.FindStringSubmatchIndex(s); m != nil && m[0] > 0 {
				setSE(m[0], 0, atoi(s[m[2]:m[3]]))
			}
		}
	}
	if pn.Episodic && pn.Season == 0 {
		pn.Season = 1
	}

	cut := len(s)
	if loc := reNoise.FindStringIndex(s); loc != nil && loc[0] > 0 && loc[0] < cut {
		cut = loc[0]
	}
	if seIdx > 0 && seIdx < cut {
		cut = seIdx
	}
	region := s[:cut]
	if ys := reYear.FindAllStringIndex(region, -1); len(ys) > 0 {
		last := ys[len(ys)-1]
		if last[0] > 0 {
			pn.Year = atoi(region[last[0]:last[1]])
			region = region[:last[0]]
		}
	}
	pn.Title = strings.Trim(strings.TrimSpace(region), "-–—:| ")
	if m := rePart.FindStringSubmatch(s); m != nil {
		pn.Part = atoi(m[1])
	}
	return pn
}

// stripPart "Rashtra Kavach Om Part 2" -> "Rashtra Kavach Om"
func stripPart(title string) string {
	t := rePart.ReplaceAllString(title, " ")
	return strings.Trim(strings.Join(strings.Fields(t), " "), "-–—:| ")
}

// ---------- TMDB client ----------

type TMDB struct {
	Key  string
	HTTP *http.Client
}

func NewTMDB(key string) *TMDB {
	return &TMDB{Key: strings.TrimSpace(key), HTTP: &http.Client{Timeout: 12 * time.Second}}
}

func (t *TMDB) Enabled() bool { return t != nil && t.Key != "" }

func (t *TMDB) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	bearer := strings.HasPrefix(t.Key, "eyJ")
	if !bearer {
		q.Set("api_key", t.Key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tmdbBase+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer {
		req.Header.Set("Authorization", "Bearer "+t.Key)
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("TMDB API key galat hai")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TMDB status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

type tmdbSearchItem struct {
	ID            int      `json:"id"`
	MediaType     string   `json:"media_type"`
	Title         string   `json:"title"`
	Name          string   `json:"name"`
	OrigTitle     string   `json:"original_title"`
	OrigName      string   `json:"original_name"`
	ReleaseDate   string   `json:"release_date"`
	FirstAirDate  string   `json:"first_air_date"`
	Popularity    float64  `json:"popularity"`
	OriginCountry []string `json:"origin_country"`
}

// FindMatch file ke naam ke hisaab se sabse sahi TMDB title dhoondhta hai.
// ok=false matlab koi bharosemand match nahi mila -> "Other" mein jayega.
func (t *TMDB) FindMatch(ctx context.Context, pn ParsedName) (kind string, id int, ok bool) {
	if !t.Enabled() || len([]rune(pn.Title)) < 2 {
		return "", 0, false
	}
	q := url.Values{}
	q.Set("query", pn.Title)
	q.Set("include_adult", "false")
	q.Set("language", "en-US")
	endpoint := "/search/multi"
	if pn.Episodic {
		endpoint = "/search/tv"
	}
	var out struct {
		Results []tmdbSearchItem `json:"results"`
	}
	if err := t.get(ctx, endpoint, q, &out); err != nil {
		return "", 0, false
	}
	want := normText(pn.Title)
	if len(want) < 2 {
		return "", 0, false
	}
	type scored struct {
		kind  string
		id    int
		score float64
	}
	var best *scored
	for _, r := range out.Results {
		mt := r.MediaType
		if pn.Episodic {
			mt = "tv"
		}
		if mt != "movie" && mt != "tv" {
			continue
		}
		nm, on, date := r.Title, r.OrigTitle, r.ReleaseDate
		if mt == "tv" {
			nm, on, date = r.Name, r.OrigName, r.FirstAirDate
		}
		a, b := normText(nm), normText(on)
		var sc float64
		switch {
		case a == want || b == want:
			sc = 100
		case len(want) >= 5 && (strings.Contains(a, want) || strings.Contains(want, a) && len(a) >= 5):
			sc = 40
		default:
			continue
		}
		yr := 0
		if len(date) >= 4 {
			yr = atoi(date[:4])
		}
		if pn.Year > 0 && yr > 0 {
			switch d := yr - pn.Year; {
			case d == 0:
				sc += 30
			case d == 1 || d == -1:
				sc += 15
			default:
				if sc < 100 {
					continue
				}
				sc -= 20
			}
		}
		pop := r.Popularity
		if pop > 50 {
			pop = 50
		}
		sc += pop / 10
		if best == nil || sc > best.score {
			best = &scored{mt, r.ID, sc}
		}
	}
	if best == nil {
		return "", 0, false
	}
	return best.kind, best.id, true
}

type tmdbDetail struct {
	ID               int      `json:"id"`
	Title            string   `json:"title"`
	Name             string   `json:"name"`
	OriginalLanguage string   `json:"original_language"`
	Overview         string   `json:"overview"`
	Tagline          string   `json:"tagline"`
	VoteAverage      float64  `json:"vote_average"`
	ReleaseDate      string   `json:"release_date"`
	FirstAirDate     string   `json:"first_air_date"`
	Runtime          int      `json:"runtime"`
	EpisodeRunTime   []int    `json:"episode_run_time"`
	NumberOfSeasons  int      `json:"number_of_seasons"`
	PosterPath       string   `json:"poster_path"`
	BackdropPath     string   `json:"backdrop_path"`
	OriginCountry    []string `json:"origin_country"`
	Genres           []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
	Collection *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"belongs_to_collection"`
	Credits struct {
		Cast []struct {
			Name        string `json:"name"`
			Character   string `json:"character"`
			ProfilePath string `json:"profile_path"`
		} `json:"cast"`
	} `json:"credits"`
	Videos struct {
		Results []struct {
			Key  string `json:"key"`
			Site string `json:"site"`
			Type string `json:"type"`
		} `json:"results"`
	} `json:"videos"`
}

// Category: Movie | Series | Anime | Cartoon  (Other tab milne par khud banta hai)
func classify(kind string, d *tmdbDetail) string {
	animation := false
	for _, g := range d.Genres {
		if g.ID == 16 || strings.EqualFold(g.Name, "Animation") {
			animation = true
		}
	}
	if animation {
		jp := d.OriginalLanguage == "ja"
		for _, c := range d.OriginCountry {
			if c == "JP" {
				jp = true
			}
		}
		if jp {
			return "Anime"
		}
		return "Cartoon"
	}
	if kind == "tv" {
		return "Series"
	}
	return "Movie"
}

// FetchTitle TMDB se poori jaankari laakar TitleDoc banata hai.
func (t *TMDB) FetchTitle(ctx context.Context, kind string, id int) (*TitleDoc, error) {
	q := url.Values{}
	q.Set("language", "en-US")
	q.Set("append_to_response", "credits,videos")
	var d tmdbDetail
	if err := t.get(ctx, fmt.Sprintf("/%s/%d", kind, id), q, &d); err != nil {
		return nil, err
	}
	td := &TitleDoc{
		Key:      fmt.Sprintf("%s:%d", kind, id),
		Kind:     kind,
		TMDBID:   id,
		Title:    d.Title,
		Overview: d.Overview,
		Tagline:  d.Tagline,
		Rating:   float64(int(d.VoteAverage*10+0.5)) / 10,
		Runtime:  d.Runtime,
		Seasons:  d.NumberOfSeasons,
	}
	date := d.ReleaseDate
	if kind == "tv" {
		td.Title = d.Name
		date = d.FirstAirDate
		if len(d.EpisodeRunTime) > 0 {
			td.Runtime = d.EpisodeRunTime[0]
		}
	}
	if len(date) >= 4 {
		td.Year = atoi(date[:4])
	}
	if td.Title == "" {
		return nil, errors.New("TMDB mein title nahi hai")
	}
	for _, g := range d.Genres {
		td.Genres = append(td.Genres, g.Name)
	}
	if d.PosterPath != "" {
		td.Poster = imgPoster + d.PosterPath
	}
	if d.BackdropPath != "" {
		td.Backdrop = imgBackdrop + d.BackdropPath
	}
	for i, c := range d.Credits.Cast {
		if i >= 12 {
			break
		}
		ph := ""
		if c.ProfilePath != "" {
			ph = imgProfile + c.ProfilePath
		}
		td.Cast = append(td.Cast, Cast{Name: c.Name, Role: c.Character, Photo: ph})
	}
	for _, v := range d.Videos.Results {
		if v.Site == "YouTube" && v.Type == "Trailer" {
			td.Trailer = v.Key
			break
		}
	}
	td.Category = classify(kind, &d)

	// movie ka agla/pichla part (collection): "second part, third part"
	if kind == "movie" && d.Collection != nil && d.Collection.ID > 0 {
		td.CollectionID = d.Collection.ID
		td.CollectionName = d.Collection.Name
		var col struct {
			Parts []struct {
				ID          int    `json:"id"`
				Title       string `json:"title"`
				ReleaseDate string `json:"release_date"`
				PosterPath  string `json:"poster_path"`
			} `json:"parts"`
		}
		cq := url.Values{}
		cq.Set("language", "en-US")
		if err := t.get(ctx, fmt.Sprintf("/collection/%d", d.Collection.ID), cq, &col); err == nil {
			for _, p := range col.Parts {
				pt := Part{TMDBID: p.ID, Title: p.Title}
				if len(p.ReleaseDate) >= 4 {
					pt.Year = atoi(p.ReleaseDate[:4])
				}
				if p.PosterPath != "" {
					pt.Poster = imgPoster + p.PosterPath
				}
				td.Parts = append(td.Parts, pt)
			}
			sort.SliceStable(td.Parts, func(i, j int) bool {
				if td.Parts[i].Year != td.Parts[j].Year {
					return td.Parts[i].Year < td.Parts[j].Year
				}
				return td.Parts[i].TMDBID < td.Parts[j].TMDBID
			})
		}
	}
	return td, nil
}

// Ping TMDB key jaanchne ke liye ek jaani-pehchani film (The Matrix) laata hai.
func (t *TMDB) Ping(ctx context.Context) (string, float64, error) {
	if !t.Enabled() {
		return "", 0, errors.New("TMDB_API_KEY set nahi hai")
	}
	var d tmdbDetail
	if err := t.get(ctx, "/movie/603", url.Values{"language": {"en-US"}}, &d); err != nil {
		return "", 0, err
	}
	return d.Title, d.VoteAverage, nil
}

var reTMDBLink = regexp.MustCompile(`(?i)(?:themoviedb\.org/|^|\s)(movie|tv)/(\d+)`)

// ParseTMDBLink "https://www.themoviedb.org/movie/603-the-matrix" -> ("movie", 603)
func ParseTMDBLink(s string) (string, int, bool) {
	m := reTMDBLink.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", 0, false
	}
	id := atoi(m[2])
	if id <= 0 {
		return "", 0, false
	}
	return strings.ToLower(m[1]), id, true
}
