// Package config loads telliebot's settings from the environment.
package config

import (
	"errors"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds runtime settings.
type Config struct {
	// BotToken is the Discord bot token. Required.
	BotToken string
	// GuildID, if set, registers slash commands to a single server so they
	// show up instantly while developing. Empty means global commands.
	GuildID string
	// TMDBAPIKey enables movies and shows. Accepts either a v3 API key or a
	// v4 read access token. Empty disables TMDB.
	TMDBAPIKey string
}

// Load reads configuration from the environment, falling back to a .env
// file in the working directory. Real environment variables take precedence.
func Load() (Config, error) {
	_ = godotenv.Load() // optional; missing .env is fine (e.g. in Docker)

	c := Config{
		BotToken:   strings.TrimPrefix(strings.TrimSpace(os.Getenv("BOT_TOKEN")), "Bot "),
		GuildID:    strings.TrimSpace(os.Getenv("GUILD_ID")),
		TMDBAPIKey: strings.TrimSpace(os.Getenv("TMDB_API_KEY")),
	}
	if c.BotToken == "" {
		return c, errors.New("BOT_TOKEN is not set")
	}
	return c, nil
}
