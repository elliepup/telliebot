// Command telliebot runs the Discord bot.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/elliepup/telliebot/internal/anilist"
	"github.com/elliepup/telliebot/internal/bot"
	"github.com/elliepup/telliebot/internal/config"
	"github.com/elliepup/telliebot/internal/pick"
	"github.com/elliepup/telliebot/internal/tmdb"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	sources := map[pick.Kind]pick.Source{
		pick.Anime: anilist.New(),
	}
	if cfg.TMDBAPIKey != "" {
		t := tmdb.New(cfg.TMDBAPIKey)
		sources[pick.Movie] = t.Movies()
		sources[pick.Show] = t.Shows()
	} else {
		slog.Info("TMDB_API_KEY not set; movies and shows are disabled")
	}

	b, err := bot.New(cfg.BotToken, cfg.GuildID, sources)
	if err != nil {
		slog.Error("create bot", "err", err)
		os.Exit(1)
	}
	if err := b.Open(); err != nil {
		slog.Error("start bot", "err", err)
		os.Exit(1)
	}
	defer b.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("telliebot is running; press Ctrl+C to stop")
	<-ctx.Done()
	slog.Info("shutting down")
}
