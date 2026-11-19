# matrix-youtube-preview

Matrix bot (Go, [mautrix-go](https://github.com/mautrix/go)) that replies to YouTube links with a preview:
thumbnail, title, channel, subscribers, views, likes, upload date, description (and duration, see below).

- Supports end-to-end encrypted rooms (thumbnails are uploaded encrypted in E2EE rooms)
- Auto-joins rooms it is invited to (including invites received while offline)
- Handles `youtube.com/watch`, `youtu.be`, `/shorts/`, `/live/`, `/embed/`, `m.` and `music.` links
- Ignores edits, notices and its own messages; doesn't reply to backlog on first start

## Configuration

```sh
cp config.example.yaml config.yaml   # set server, user, password
```

Only `server`, `user` and `password` are required. Keep `data/` (the `database`) persistent: it holds
the bot's device and E2EE keys.

## YouTube and the bot check

YouTube answers server-side requests with *"Sign in to confirm you're not a bot"*. Tested Nov 2026:

| Source | Without cookies | Provides |
|---|---|---|
| oEmbed API | ✅ works | title, channel, thumbnail |
| Watch page `ytInitialData` | ✅ works | views, likes, date, subscribers, description |
| Watch page `ytInitialPlayerResponse` | ❌ `LOGIN_REQUIRED` | duration |

So the preview works without cookies; cookies only add the **duration**. To use them, export a
Netscape-format `cookies.txt` from a browser logged into YouTube (preferably a throwaway account),
e.g. with the "Get cookies.txt LOCALLY" extension or
`yt-dlp --cookies-from-browser chrome --cookies data/cookies.txt --skip-download <url>`,
then set `youtube.cookies_file: /data/cookies.txt` and `youtube.user_agent` to that browser's user agent.

Test a link without connecting to Matrix:

```sh
go run -tags goolm . -config config.yaml -test-url 'https://www.youtube.com/watch?v=tqyCgCs4WrY'
```

## Running

```sh
mkdir -p data && sudo chown 65532:65532 data   # image runs as distroless nonroot
docker compose up -d
```

Images are built by GitHub Actions on every push and published to
`ghcr.io/ricardo-duarte-av/matrix-youtube-preview` (`latest` for `main`, plus branch and `sha-` tags).

## Building locally

```sh
CGO_ENABLED=1 go build -tags goolm .
```

`goolm` selects the pure-Go olm implementation, so libolm isn't needed. CGO is still required for SQLite.
