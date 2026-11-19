package main

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/ricardo-duarte-av/matrix-youtube-preview/internal/youtube"
)

type Bot struct {
	client    *mautrix.Client
	yt        *youtube.Client
	cfg       *Config
	log       zerolog.Logger
	startTime time.Time
}

func (b *Bot) handleMember(ctx context.Context, evt *event.Event) {
	if evt.GetStateKey() != b.client.UserID.String() {
		return
	}
	if evt.Content.AsMember().Membership == event.MembershipInvite {
		b.join(ctx, evt.RoomID, evt.Sender)
	}
}

func (b *Bot) join(ctx context.Context, roomID id.RoomID, inviter id.UserID) {
	log := b.log.With().Stringer("room_id", roomID).Stringer("inviter", inviter).Logger()
	if _, err := b.client.JoinRoomByID(ctx, roomID); err != nil {
		log.Error().Err(err).Msg("Failed to join room")
		return
	}
	log.Info().Msg("Joined room after invite")
}

func (b *Bot) handleMessage(ctx context.Context, evt *event.Event) {
	if evt.Sender == b.client.UserID {
		return
	}
	// Ignore anything much older than our startup (e.g. replays after a long outage).
	if time.UnixMilli(evt.Timestamp).Before(b.startTime.Add(-time.Hour)) {
		return
	}
	msg := evt.Content.AsMessage()
	// Ignore notices (other bots) and edits (the original was already handled).
	if msg.MsgType != event.MsgText && msg.MsgType != event.MsgEmote {
		return
	}
	if msg.RelatesTo != nil && msg.RelatesTo.Type == event.RelReplace {
		return
	}

	text := msg.Body
	if msg.Format == event.FormatHTML {
		// Links in formatted bodies may be hidden behind link text.
		text += " " + msg.FormattedBody
	}
	ids := youtube.ExtractIDs(stripReplyFallback(text))
	if len(ids) == 0 {
		return
	}
	if len(ids) > b.cfg.MaxLinksPerMessage {
		ids = ids[:b.cfg.MaxLinksPerMessage]
	}
	// The syncer runs handlers sequentially; don't block it on network I/O.
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		for _, vid := range ids {
			b.preview(ctx, evt, vid)
		}
	}()
}

// stripReplyFallback removes legacy "> quoted" reply fallbacks so we don't
// re-preview links from the message being replied to.
func stripReplyFallback(s string) string {
	if i := strings.Index(s, "</mx-reply>"); i >= 0 {
		s = s[i+len("</mx-reply>"):]
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "> ") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func (b *Bot) preview(ctx context.Context, evt *event.Event, vid string) {
	log := b.log.With().Stringer("room_id", evt.RoomID).Stringer("event_id", evt.ID).Str("video_id", vid).Logger()
	_, _ = b.client.UserTyping(ctx, evt.RoomID, true, 15*time.Second)
	defer func() { _, _ = b.client.UserTyping(ctx, evt.RoomID, false, 0) }()

	v, err := b.yt.Fetch(ctx, vid)
	if err != nil {
		log.Warn().Err(err).Msg("Could not fetch video metadata")
		return
	}

	var content *event.MessageEventContent
	if *b.cfg.SendThumbnail {
		content, err = b.imageContent(ctx, evt.RoomID, v)
		if err != nil {
			log.Warn().Err(err).Msg("Thumbnail failed, sending text-only preview")
		}
	}
	if content == nil {
		content = &event.MessageEventContent{MsgType: event.MsgNotice}
	}
	content.Body = v.Plain()
	content.Format = event.FormatHTML
	content.FormattedBody = v.HTML()
	content.RelatesTo = (&event.RelatesTo{}).SetReplyTo(evt.ID)
	// Don't ping the person who posted the link.
	content.Mentions = &event.Mentions{}

	if _, err := b.client.SendMessageEvent(ctx, evt.RoomID, event.EventMessage, content); err != nil {
		log.Error().Err(err).Msg("Failed to send preview")
		return
	}
	log.Info().Str("title", v.Title).Dur("duration", v.Duration).Msg("Sent preview")
}

// imageContent downloads the best available thumbnail, uploads it (encrypted
// if the room is encrypted) and returns an m.image content. The caption
// (body/formatted_body) is filled in by the caller.
func (b *Bot) imageContent(ctx context.Context, roomID id.RoomID, v *youtube.Video) (*event.MessageEventContent, error) {
	var data []byte
	var err error
	for _, u := range v.ThumbnailCandidates() {
		if data, err = b.yt.GetImage(ctx, u); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	mime := http.DetectContentType(data)
	info := &event.FileInfo{MimeType: mime, Size: len(data)}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		info.Width, info.Height = cfg.Width, cfg.Height
	}
	ext := ".jpg"
	if mime == "image/png" {
		ext = ".png"
	}
	content := &event.MessageEventContent{
		MsgType:  event.MsgImage,
		FileName: "youtube-" + v.ID + ext,
		Info:     info,
	}

	encrypted, err := b.client.StateStore.IsEncrypted(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if encrypted {
		file := attachment.NewEncryptedFile()
		file.EncryptInPlace(data)
		resp, err := b.client.UploadBytes(ctx, data, "application/octet-stream")
		if err != nil {
			return nil, err
		}
		content.File = &event.EncryptedFileInfo{EncryptedFile: *file, URL: resp.ContentURI.CUString()}
	} else {
		resp, err := b.client.UploadBytesWithName(ctx, data, mime, content.FileName)
		if err != nil {
			return nil, err
		}
		content.URL = resp.ContentURI.CUString()
	}
	return content, nil
}
