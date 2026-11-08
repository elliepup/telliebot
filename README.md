# telliebot

A Discord bot that picks something random to watch (anime, a movie, or a show) based on how
you're feeling. Named after Ellie. 🐾

## Usage

```
/pick mood:<mood> type:<type>
```

Both options are optional. `mood` defaults to **Surprise me** and `type` defaults to
**Anything**. Every pick has a 🔄 **Reroll** button that picks again with the same settings.

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

With Docker Compose:

```sh
docker compose up -d --build
docker compose logs -f
```

With plain Docker:

```sh
docker build -t telliebot .
docker run -d --name telliebot --env-file .env --restart unless-stopped telliebot
```

Locally:

```sh
go run ./cmd/telliebot
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the branch and commit message rules.
