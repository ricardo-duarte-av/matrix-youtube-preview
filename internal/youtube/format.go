package youtube

import (
	"fmt"
	"html"
	"strings"
	"time"
)

const maxDescription = 300

// FormatDuration renders 1h02m03s as "1:02:03" and 3m05s as "3:05".
func FormatDuration(d time.Duration) string {
	s := int(d.Seconds())
	h, m, sec := s/3600, (s/60)%60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

func (v *Video) statsLine() string {
	var parts []string
	if v.IsLive {
		parts = append(parts, "🔴 LIVE")
	} else if v.Duration > 0 {
		parts = append(parts, "⏱ "+FormatDuration(v.Duration))
	}
	if v.Views != "" {
		parts = append(parts, "👁 "+v.Views)
	}
	if v.Likes != "" {
		parts = append(parts, "👍 "+v.Likes)
	}
	if v.Published != "" {
		parts = append(parts, "📅 "+v.Published)
	}
	return strings.Join(parts, " · ")
}

func (v *Video) channelLine() string {
	if v.Channel == "" {
		return ""
	}
	if v.Subscribers != "" {
		return v.Channel + " (" + v.Subscribers + ")"
	}
	return v.Channel
}

func (v *Video) shortDescription() string {
	d := strings.TrimSpace(v.Description)
	// Keep at most 4 non-empty lines.
	var lines []string
	for _, l := range strings.Split(d, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
		if len(lines) == 4 {
			break
		}
	}
	d = strings.Join(lines, "\n")
	if r := []rune(d); len(r) > maxDescription {
		d = strings.TrimSpace(string(r[:maxDescription])) + "…"
	}
	return d
}

// Plain returns a plain-text preview.
func (v *Video) Plain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "▶ %s\n", v.Title)
	if c := v.channelLine(); c != "" {
		fmt.Fprintf(&b, "📺 %s\n", c)
	}
	if s := v.statsLine(); s != "" {
		fmt.Fprintf(&b, "%s\n", s)
	}
	if d := v.shortDescription(); d != "" {
		for _, l := range strings.Split(d, "\n") {
			fmt.Fprintf(&b, "> %s\n", l)
		}
	}
	b.WriteString(v.URL)
	return b.String()
}

// HTML returns an org.matrix.custom.html formatted preview.
func (v *Video) HTML() string {
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `▶ <strong><a href="%s">%s</a></strong>`, e(v.URL), e(v.Title))
	if c := v.channelLine(); c != "" {
		if v.ChannelURL != "" {
			fmt.Fprintf(&b, `<br>📺 <a href="%s">%s</a>`, e(v.ChannelURL), e(c))
		} else {
			fmt.Fprintf(&b, "<br>📺 %s", e(c))
		}
	}
	if s := v.statsLine(); s != "" {
		fmt.Fprintf(&b, "<br>%s", e(s))
	}
	if d := v.shortDescription(); d != "" {
		fmt.Fprintf(&b, "<blockquote>%s</blockquote>", strings.ReplaceAll(e(d), "\n", "<br>"))
	}
	return b.String()
}
