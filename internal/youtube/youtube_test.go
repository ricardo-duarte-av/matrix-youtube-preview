package youtube

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestVideoID(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=tqyCgCs4WrY":                 "tqyCgCs4WrY",
		"https://youtube.com/watch?feature=share&v=tqyCgCs4WrY&t=10s": "tqyCgCs4WrY",
		"https://m.youtube.com/watch?v=tqyCgCs4WrY":                   "tqyCgCs4WrY",
		"https://music.youtube.com/watch?v=tqyCgCs4WrY&list=RD":       "tqyCgCs4WrY",
		"https://youtu.be/tqyCgCs4WrY?si=abc":                         "tqyCgCs4WrY",
		"https://www.youtube.com/shorts/tqyCgCs4WrY":                  "tqyCgCs4WrY",
		"https://www.youtube.com/live/tqyCgCs4WrY?feature=shared":     "tqyCgCs4WrY",
		"https://www.youtube.com/embed/tqyCgCs4WrY":                   "tqyCgCs4WrY",
		"https://www.youtube-nocookie.com/embed/tqyCgCs4WrY":          "tqyCgCs4WrY",
		"https://www.youtube.com/@SomeChannel":                        "",
		"https://www.youtube.com/watch?v=short":                       "",
		"https://example.com/watch?v=tqyCgCs4WrY":                     "",
	}
	for in, want := range cases {
		if got := VideoID(in); got != want {
			t.Errorf("VideoID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractIDs(t *testing.T) {
	text := `look at this https://youtu.be/tqyCgCs4WrY and (https://www.youtube.com/watch?v=dQw4w9WgXcQ)
again https://www.youtube.com/watch?v=tqyCgCs4WrY, <a href="https://youtube.com/shorts/aaaaaaaaaaa">x</a>`
	want := []string{"tqyCgCs4WrY", "dQw4w9WgXcQ", "aaaaaaaaaaa"}
	if got := ExtractIDs(text); !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractIDs = %v, want %v", got, want)
	}
}

func TestFormatDuration(t *testing.T) {
	if got := FormatDuration(3*time.Minute + 5*time.Second); got != "3:05" {
		t.Error(got)
	}
	if got := FormatDuration(time.Hour + 2*time.Minute + 3*time.Second); got != "1:02:03" {
		t.Error(got)
	}
}

func TestLoadNetscapeCookies(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cookies.txt")
	data := "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tabc\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tLOGIN_INFO\tdef\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSID\tother\n"
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadNetscapeCookies(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 2 || c[0].Name != "SID" || c[1].Name != "LOGIN_INFO" || c[1].Value != "def" {
		t.Errorf("unexpected cookies: %+v", c)
	}
}

func TestWithEnglishPref(t *testing.T) {
	if got := withEnglishPref("f6=40000000&hl=pt-PT&tz=Europe.Lisbon"); got != "f6=40000000&tz=Europe.Lisbon&hl=en&gl=US" {
		t.Error(got)
	}
	if got := withEnglishPref(""); got != "hl=en&gl=US" {
		t.Error(got)
	}
}

func TestGroupThousands(t *testing.T) {
	for in, want := range map[string]string{"7": "7", "7424": "7,424", "1234567": "1,234,567", "100": "100"} {
		if got := groupThousands(in); got != want {
			t.Errorf("%s -> %s", in, got)
		}
	}
}
