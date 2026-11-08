# telliebot

A Discord bot that picks something random to watch (anime, a movie, or a show) based on how
you're feeling. Named after Ellie. 🐾

## Usage

```
/pick mood:<mood> type:<type> min_rating:<rating> length:<length> era:<era>
```

Every option is optional. Every pick has a 🔄 **Reroll** button that picks again with the same
settings.

| Option       | Choices                                         | Default       |
|--------------|-------------------------------------------------|---------------|
| `mood`       | See below                                       | Surprise me   |
| `type`       | Anything, Anime, Movie, Show                    | Anything      |
| `min_rating` | 6+, 7+, 7.5+, 8+, 8.5+ (out of 10)              | 6.5+          |
| `length`     | Short, Medium, Long                             | Any           |
| `era`        | Classic (before 2000), 2000s, 2010s, Recent (last 3 years) | Any |

`length` means something different for each type:

| Length | Movies      | Anime      | Shows (per episode) |
|--------|-------------|------------|---------------------|
| Short  | ≤100 min    | ≤13 eps    | ≤30 min             |
| Medium | 101–140 min | 14–26 eps  | 31–50 min           |
| Long   | 141+ min    | 27+ eps    | 51+ min             |

Shows filter by episode runtime because TMDB can't filter by number of seasons.

### Buttons

Every pick has these buttons:

| Button      | What it does                                                                 |
|-------------|------------------------------------------------------------------------------|
| 🔄 Reroll   | Pick again with the same settings                                            |
| 👁️ Seen it  | Never suggest this title again in this server, then pick a new one. The person who clicked gets a private **Undo** |
| ⭐ Save     | Add it to the server's shared watchlist                                      |
| ▶️ Trailer  | Open the trailer (when one is known)                                         |

### Watchlist

The seen list and watchlist are **shared by everyone in a server**. In DMs, they're personal.

- `/watchlist [type]` shows the 25 newest saved titles, with a dropdown to remove them
- `/pick from:watchlist [type]` picks a random title from the watchlist (mood and filters are ignored)
- Marking a saved title as seen also removes it from the watchlist

Moods: Surprise me, Cozy, Funny, Hype, Emotional, Romantic, Spooky, Mind-bending, Epic
adventure, Tense. The genre mapping for each lives in [`internal/mood/mood.go`](internal/mood/mood.go).

| Type   | Source                                   | Needs a key?          |
|--------|------------------------------------------|-----------------------|
| Anime  | [AniList](https://anilist.co)            | No                    |
| Movie  | [TMDB](https://www.themoviedb.org)       | Yes, `TMDB_API_KEY`   |
| Show   | [TMDB](https://www.themoviedb.org)       | Yes, `TMDB_API_KEY`   |

Movies and shows only appear in `/pick` once `TMDB_API_KEY` is set.

## Setup

1. Create an application in the [Discord Developer Portal](https://discord.com/developers/applications).
   Under **Bot**, reset and copy the token.
2. Invite the bot. Under **OAuth2 → URL Generator**, select the `bot` and
   `applications.commands` scopes, then open the generated URL.
3. Copy `.env.example` to `.env` and fill it in. Set `GUILD_ID` to your server's ID while
   developing so command changes show up instantly.

## Running

With Docker Compose (use `docker compose` instead if you have the Compose plugin):

```sh
docker-compose up -d --build
docker-compose logs -f
```

With plain Docker:

```sh
docker build -t telliebot .
docker run -d --name telliebot --env-file .env --restart unless-stopped \
  -v telliebot-data:/data telliebot
```

The database lives in the `telliebot-data` volume, so it survives rebuilds. To back it up:

```sh
docker run --rm -v telliebot_telliebot-data:/data -v "$PWD":/backup alpine \
  cp /data/telliebot.db /backup/telliebot-backup.db
```

(Compose prefixes the volume name with the project name. Check it with `docker volume ls`.)

Locally:

```sh
go run ./cmd/telliebot
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the branch and commit message rules.
