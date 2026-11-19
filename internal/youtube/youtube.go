// Package youtube extracts video metadata for link previews.
//
// Strategy (tested against YouTube in Nov 2026):
//   - oEmbed (https://www.youtube.com/oembed) works without cookies and gives
//     title, channel and thumbnail. It is the reliable baseline.
//   - The watch page HTML embeds ytInitialData, which still contains view count,
//     upload date, likes, channel subscriber count and the description even
//     when YouTube shows "Sign in to confirm you're not a bot".
//   - ytInitialPlayerResponse (duration, keywords, ...) is blocked by that bot
//     check (playabilityStatus LOGIN_REQUIRED) unless valid cookies are sent.
//     Duration is therefore only shown when cookies are configured and accepted.
package youtube

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

// Video holds everything we could find out about a video. Fields other than
// ID, URL and Title may be empty.
type Video struct {
	ID           string
	URL          string
	Title        string
	Channel      string
	ChannelURL   string
	Subscribers  string // e.g. "19.6K subscribers"
	Views        string // e.g. "182,215 views"
	Likes        string // e.g. "7,410"
	Published    string // e.g. "Nov 15, 2026"
	Duration     time.Duration
	IsLive       bool
	Description  string
	ThumbnailURL string // best guess; see ThumbnailCandidates
}

// ThumbnailCandidates returns thumbnail URLs from best to worst quality.
func (v *Video) ThumbnailCandidates() []string {
	c := []string{
		"https://i.ytimg.com/vi/" + v.ID + "/maxresdefault.jpg",
		"https://i.ytimg.com/vi/" + v.ID + "/sddefault.jpg",
		"https://i.ytimg.com/vi/" + v.ID + "/hqdefault.jpg",
	}
	if v.ThumbnailURL != "" {
		c = append(c, v.ThumbnailURL)
	}
	return c
}

type Client struct {
	HTTP      *http.Client
	UserAgent string
	cookies   []*http.Cookie
}

// NewClient creates a client. cookiesFile is an optional Netscape cookies.txt.
func NewClient(userAgent, cookiesFile string) (*Client, error) {
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	c := &Client{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		UserAgent: userAgent,
	}
	if cookiesFile != "" {
		cookies, err := loadNetscapeCookies(cookiesFile)
		if err != nil {
			return nil, fmt.Errorf("load cookies: %w", err)
		}
		c.cookies = cookies
	}
	return c, nil
}

func (c *Client) HasCookies() bool { return len(c.cookies) > 0 }

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// linkRe finds candidate YouTube URLs in free text.
var linkRe = regexp.MustCompile(`(?i)\bhttps?://(?:www\.|m\.|music\.)?(?:youtube\.com|youtu\.be|youtube-nocookie\.com)/[^\s<>"')\]]*`)

// ExtractIDs returns unique video IDs from the YouTube links in text, in order.
func ExtractIDs(text string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, raw := range linkRe.FindAllString(text, -1) {
		id := VideoID(raw)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// VideoID extracts the video ID from a single YouTube URL, or "" if none.
func VideoID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var id string
	switch host {
	case "youtu.be":
		id = parts[0]
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		switch parts[0] {
		case "watch":
			id = u.Query().Get("v")
		case "shorts", "live", "embed", "v", "e":
			if len(parts) > 1 {
				id = parts[1]
			}
		}
	}
	if idRe.MatchString(id) {
		return id
	}
	return ""
}

// Fetch gathers metadata for a video ID. It returns an error only if not even
// the basic oEmbed data could be retrieved.
func (c *Client) Fetch(ctx context.Context, id string) (*Video, error) {
	v := &Video{ID: id, URL: "https://www.youtube.com/watch?v=" + id}
	oerr := c.fetchOEmbed(ctx, v)
	werr := c.fetchWatchPage(ctx, v)
	if v.Title == "" {
		return nil, fmt.Errorf("no metadata for %s: oembed: %v; watch page: %v", id, oerr, werr)
	}
	return v, nil
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// SOCS=CAI is the "reject all" answer to the EU consent banner, so the
	// real page is served instead of a redirect to consent.youtube.com.
	req.AddCookie(&http.Cookie{Name: "SOCS", Value: "CAI"})
	pref := ""
	for _, ck := range c.cookies {
		switch ck.Name {
		case "SOCS":
		case "PREF":
			pref = ck.Value
		default:
			req.AddCookie(ck)
		}
	}
	// A logged-in account's language overrides the hl= URL parameter, but hl
	// inside the PREF cookie wins. Force English so the output is consistent.
	req.AddCookie(&http.Cookie{Name: "PREF", Value: withEnglishPref(pref)})
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}

// withEnglishPref sets hl=en and gl=US in a PREF cookie value, keeping other keys.
func withEnglishPref(pref string) string {
	var kept []string
	for _, kv := range strings.Split(pref, "&") {
		if kv != "" && !strings.HasPrefix(kv, "hl=") && !strings.HasPrefix(kv, "gl=") {
			kept = append(kept, kv)
		}
	}
	return strings.Join(append(kept, "hl=en", "gl=US"), "&")
}

// GetImage downloads an image (used for thumbnails).
func (c *Client) GetImage(ctx context.Context, rawURL string) ([]byte, error) {
	return c.get(ctx, rawURL)
}

func (c *Client) fetchOEmbed(ctx context.Context, v *Video) error {
	q := url.Values{"url": {v.URL}, "format": {"json"}}
	body, err := c.get(ctx, "https://www.youtube.com/oembed?"+q.Encode())
	if err != nil {
		return err
	}
	var o struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		AuthorURL    string `json:"author_url"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.Unmarshal(body, &o); err != nil {
		return err
	}
	v.Title, v.Channel, v.ChannelURL, v.ThumbnailURL = o.Title, o.AuthorName, o.AuthorURL, o.ThumbnailURL
	return nil
}

// --- watch page parsing ---

type text struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text string `json:"text"`
	} `json:"runs"`
}

func (t text) String() string {
	if t.SimpleText != "" {
		return t.SimpleText
	}
	var b strings.Builder
	for _, r := range t.Runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

type initialData struct {
	Contents struct {
		TwoColumnWatchNextResults struct {
			Results struct {
				Results struct {
					Contents []struct {
						Primary *struct {
							Title     text `json:"title"`
							ViewCount struct {
								Renderer struct {
									ViewCount text `json:"viewCount"`
									IsLive    bool `json:"isLive"`
								} `json:"videoViewCountRenderer"`
							} `json:"viewCount"`
							DateText text `json:"dateText"`
						} `json:"videoPrimaryInfoRenderer"`
						Secondary *struct {
							Owner struct {
								Renderer struct {
									Title           text `json:"title"`
									SubscriberCount text `json:"subscriberCountText"`
								} `json:"videoOwnerRenderer"`
							} `json:"owner"`
							AttributedDescription struct {
								Content string `json:"content"`
							} `json:"attributedDescription"`
						} `json:"videoSecondaryInfoRenderer"`
					} `json:"contents"`
				} `json:"results"`
			} `json:"results"`
		} `json:"twoColumnWatchNextResults"`
	} `json:"contents"`
}

type playerResponse struct {
	PlayabilityStatus struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
	VideoDetails struct {
		Title            string `json:"title"`
		Author           string `json:"author"`
		LengthSeconds    string `json:"lengthSeconds"`
		ViewCount        string `json:"viewCount"`
		ShortDescription string `json:"shortDescription"`
		IsLiveContent    bool   `json:"isLiveContent"`
	} `json:"videoDetails"`
	Microformat struct {
		Renderer struct {
			PublishDate string `json:"publishDate"`
		} `json:"playerMicroformatRenderer"`
	} `json:"microformat"`
}

// likesRe matches the accessibility label of the like button.
var likesRe = regexp.MustCompile(`like this video along with ([0-9][0-9,.]*) other`)

// likeCountRe is a language-independent fallback (raw number).
var likeCountRe = regexp.MustCompile(`"likeCount":"([0-9]+)"`)

// groupThousands turns "1234567" into "1,234,567".
func groupThousands(n string) string {
	for i := len(n) - 3; i > 0; i -= 3 {
		n = n[:i] + "," + n[i:]
	}
	return n
}

// ErrBotCheck is reported (non-fatally) when the player data is withheld.
var ErrBotCheck = errors.New("youtube bot check: player data withheld")

func (c *Client) fetchWatchPage(ctx context.Context, v *Video) error {
	body, err := c.get(ctx, v.URL+"&hl=en&gl=US")
	if err != nil {
		return err
	}

	var botErr error
	var pr playerResponse
	if extractJSON(body, "var ytInitialPlayerResponse = ", &pr) == nil {
		if pr.PlayabilityStatus.Status == "OK" {
			if n, err := strconv.Atoi(pr.VideoDetails.LengthSeconds); err == nil && n > 0 {
				v.Duration = time.Duration(n) * time.Second
			}
			v.IsLive = v.IsLive || pr.VideoDetails.IsLiveContent && v.Duration == 0
			if v.Title == "" {
				v.Title, v.Channel = pr.VideoDetails.Title, pr.VideoDetails.Author
			}
			if v.Description == "" {
				v.Description = pr.VideoDetails.ShortDescription
			}
		} else {
			botErr = fmt.Errorf("%w: %s (%s)", ErrBotCheck, pr.PlayabilityStatus.Status, pr.PlayabilityStatus.Reason)
		}
	}

	var raw json.RawMessage
	if err := extractJSON(body, "var ytInitialData = ", &raw); err != nil {
		return errors.Join(fmt.Errorf("ytInitialData: %w", err), botErr)
	}
	var d initialData
	if err := json.Unmarshal(raw, &d); err != nil {
		return errors.Join(fmt.Errorf("ytInitialData: %w", err), botErr)
	}
	for _, item := range d.Contents.TwoColumnWatchNextResults.Results.Results.Contents {
		if p := item.Primary; p != nil {
			if t := p.Title.String(); t != "" && v.Title == "" {
				v.Title = t
			}
			v.Views = p.ViewCount.Renderer.ViewCount.String()
			v.IsLive = v.IsLive || p.ViewCount.Renderer.IsLive
			v.Published = p.DateText.String()
		}
		if s := item.Secondary; s != nil {
			o := s.Owner.Renderer
			if v.Channel == "" {
				v.Channel = o.Title.String()
			}
			v.Subscribers = o.SubscriberCount.String()
			if desc := s.AttributedDescription.Content; desc != "" {
				v.Description = desc
			}
		}
	}
	if m := likesRe.FindSubmatch(raw); m != nil {
		v.Likes = string(m[1])
	} else if m := likeCountRe.FindSubmatch(raw); m != nil {
		v.Likes = groupThousands(string(m[1]))
	}
	return botErr
}

// extractJSON decodes the JSON value that directly follows marker in body.
func extractJSON(body []byte, marker string, out any) error {
	i := bytes.Index(body, []byte(marker))
	if i < 0 {
		return fmt.Errorf("marker %q not found", strings.TrimSpace(marker))
	}
	return json.NewDecoder(bytes.NewReader(body[i+len(marker):])).Decode(out)
}

// loadNetscapeCookies parses a cookies.txt file (as exported by browser
// extensions or `yt-dlp --cookies`), keeping only YouTube/Google cookies.
func loadNetscapeCookies(path string) ([]*http.Cookie, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []*http.Cookie
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// "#HttpOnly_" prefixed lines are real cookies, other # lines are comments.
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		domain := strings.TrimPrefix(f[0], ".")
		if domain != "youtube.com" && !strings.HasSuffix(domain, ".youtube.com") {
			continue
		}
		out = append(out, &http.Cookie{Name: f[5], Value: f[6]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no youtube.com cookies found in file")
	}
	return out, nil
}
