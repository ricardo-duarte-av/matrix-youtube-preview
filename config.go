package main

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Homeserver base URL, e.g. https://matrix.example.org
	Server string `yaml:"server"`
	// Full MXID (@bot:example.org) or just the localpart.
	User     string `yaml:"user"`
	Password string `yaml:"password"`

	// SQLite database for the crypto (E2EE) and state store.
	Database string `yaml:"database"`
	// Key used to encrypt the crypto keys at rest. Do not change after first run.
	PickleKey  string `yaml:"pickle_key"`
	DeviceName string `yaml:"device_name"`

	YouTube struct {
		// Optional Netscape-format cookies.txt to get past YouTube's bot check.
		CookiesFile string `yaml:"cookies_file"`
		// Optional; should match the browser the cookies came from.
		UserAgent string `yaml:"user_agent"`
	} `yaml:"youtube"`

	// Upload the thumbnail and send the preview as an image with caption.
	// If false, a text-only notice is sent.
	SendThumbnail *bool `yaml:"send_thumbnail"`
	// Maximum number of previews per message.
	MaxLinksPerMessage int `yaml:"max_links_per_message"`
	// Log level: debug, info, warn, error.
	LogLevel string `yaml:"log_level"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Server == "" || c.User == "" || c.Password == "" {
		return nil, errors.New("config: server, user and password are required")
	}
	if c.Database == "" {
		c.Database = "data/bot.db"
	}
	if c.PickleKey == "" {
		c.PickleKey = "matrix-youtube-preview"
	}
	if c.DeviceName == "" {
		c.DeviceName = "YouTube Preview Bot"
	}
	if c.SendThumbnail == nil {
		t := true
		c.SendThumbnail = &t
	}
	if c.MaxLinksPerMessage <= 0 {
		c.MaxLinksPerMessage = 3
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	return &c, nil
}
