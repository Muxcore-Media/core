# Module Interoperability Design

- **Status:** Draft
- **Scope:** Official metadata schema & communication conventions for MuxCore modules
- **Applies to:** All modules that want to interoperate with official MuxCore modules

Core is intentionally agnostic to media types and metadata schemas. It does not
know what a "movie" is, what an "IMDB" identifier looks like, or how to
interpret the `Meta` bag on a workflow step. This is by design -- core is the
loom, modules are the threads.

But for modules to interoperate, they must agree on what field names, event
types, and calling conventions to use. Without that agreement, one module stores
`"IMDB"`, another expects `"imdb_id"`, and a third writes `"ID_IMDB"` -- and
nothing works together.

This document defines the **official schema** that the MuxCore project's own
modules follow. Third-party developers may:

1. **Follow the schema** -- their modules interoperate with official modules
   and other compliant third-party modules.
2. **Extend the schema** -- add custom keys under a module-specific namespace
   (e.g., `mycustommodule_*`) while keeping official fields intact.
3. **Ignore the schema entirely** -- build a fully private module ecosystem
   that never interacts with official modules.

The schema is a convention, not a framework enforcement. Core will never
validate it, enforce it, or reject non-compliant data.

---

## 1. Universal Conventions

Every named value in the MuxCore module ecosystem follows these rules:

### 1.1 Casing

| Context | Convention | Examples |
|---------|-----------|----------|
| Map/JSON keys | `snake_case` | `imdb_id`, `vote_average`, `poster_url` |
| Event types | `domain.action` dotted | `media.movie.imported` |
| Capabilities | `domain.sub` dotted | `metadata.themoviedb` |
| Module IDs | `role-impl` hyphenated | `metadata-tmdb`, `downloader-qbittorrent` |
| Module roles | `snake_case` | `metadata`, `media_manager` |
| Config keys | `snake_case` | `download_path`, `max_connections` |
| Enums / statuses | lowercase single | `running`, `degraded`, `pending` |
| Error codes | `snake_case` | `rate_limit_exceeded`, `not_found` |

### 1.2 Value Types

Since core metadata travels through `map[string]any` and `map[string]string`,
all values are either strings or arrays of strings. Use string representations:

| Go Type | String Encoding | Example |
|---------|----------------|---------|
| `int`, `int64` | `strconv.FormatInt` | `"550"` |
| `float64` | `strconv.FormatFloat` | `"8.7"` |
| `bool` | `"true"` / `"false"` | `"true"` |
| `[]string` | JSON array | `["Action","Drama"]` |
| `[]struct` | JSON array of objects | `[{"name":"..."}]` |
| `time.Time` | RFC 3339 / ISO 8601 | `"1994-10-15"` |
| `uuid.UUID` | standard hex | `"a1b2c3d4-..."` |

Arrays should be stored as JSON-encoded strings only when order matters or you
need complex nested data. For simple single-value fields, use the plain string
form.

### 1.3 Namespace Ownership

To avoid key collisions when two modules store unrelated data in the same map:

- **Official fields** (this document) use unprefixed keys: `title`, `imdb_id`
- **Module-specific fields** should prefix with `module_name_`: `mymodule_foo`
- **Contract-defined fields** (from a contract repo) use the contract's agreed
  prefix or follow the contract's naming spec

When reading metadata, modules should gracefully handle missing keys and ignore
keys they don't understand.

### 1.4 Structural Disambiguation

The same key name can safely appear in different structural contexts. The
containing object or array always determines the meaning:

```
networks           # array of organization objects
  └─ wikidata_id   #   → identifies an organization

directors          # array of person objects
  └─ wikidata_id   #   → identifies a person

studios            # array of company objects
  └─ wikidata_id   #   → identifies a company
```

A top-level scalar key (`imdb_id: "tt0111161"`) refers to the media item
itself. The same key inside a `cast` entry refers to that person. No key
collision can occur because the paths differ:

```json
{
  "imdb_id": "tt0111161",
  "cast": [
    {"name": "Tim Robbins", "imdb_name_id": "nm0000209"}
  ],
  "production_companies": [
    {"name": "Castle Rock", "wikidata_id": "Q1050814"}
  ]
}
```

All standard identifier keys defined in Section 2 follow this rule: they may
appear at any level of nesting, disambiguated by their parent context.

---

## 2. External Identifier Fields

The most common source of incompatibility is identifier fields. Different
sources call the same thing differently. This section standardizes the key
names.

### 2.1 Media Identifiers

```text
Key              | Source        | Content-Type       | Example Value
-----------------|---------------|--------------------|------------------------
imdb_id          | IMDB          | movie, tv          | "tt0111161"
tmdb_id          | TMDB          | movie, tv          | "550"
tvdb_id          | TVDB          | tv                 | "12345"
tvmaze_id        | TVMaze        | tv                 | "12345"
anidb_id         | AniDB         | anime              | "12345"
mal_id           | MyAnimeList   | anime              | "12345"
anisearch_id     | aniSearch     | anime              | "12345"
livechart_id     | LiveChart     | anime              | "12345"
imdb_episode_id  | IMDB          | tv episode         | "tt1234567"
tmdb_episode_id  | TMDB          | tv episode         | "12345"
tvdb_episode_id  | TVDB          | tv episode         | "12345"
tvmaze_episode_id| TVMaze        | tv episode         | "12345"
```

### 2.2 Music Identifiers

```text
Key              | Source        | Domain             | Example Value
-----------------|---------------|--------------------|------------------------
musicbrainz_id   | MusicBrainz   | release/recording  | UUID
isrc             | IFPI          | track recording    | "USUMV2200001"
discogs_id       | Discogs       | release            | "12345"
spotify_id       | Spotify       | track/album/artist | "spotify:track:xxx"
spotify_uri      | Spotify       | track/album/artist | "6rqhFgbbKwnb9MLmUQDhG6"
apple_music_id   | Apple Music   | song/album/artist  | "1234567890"
deezer_id        | Deezer        | track/album/artist | "12345"
bandcamp_id      | Bandcamp      | album/track        | "12345"
upc              | GS1           | release (barcode)  | "123456789012"
catalog_number   | Label         | release            | "ABC-12345"
tidal_id         | Tidal         | track/album/artist | "12345"
```

### 2.3 Book / Written Identifiers

```text
Key              | Source        | Domain             | Example Value
-----------------|---------------|--------------------|------------------------
isbn_13          | ISBN Agency   | book               | "978-0-000-000000-0"
isbn_10          | ISBN Agency   | book               | "0-000-00000-0"
asin             | Amazon        | book/product       | "B000000000"
oclc_id          | OCLC          | work               | "123456789"
openlibrary_id   | Open Library  | work/edition       | "OL12345M"
goodreads_id     | Goodreads     | book               | "12345"
librarything_id  | LibraryThing  | book               | "12345"
google_books_id  | Google Books  | volume             | "abc123def"
lccn             | Library of Congress | work          | "12345678"
doi              | DOI Foundation | article/work      | "10.0000/12345"
wikidata_id      | Wikidata      | any entity         | "Q12345"
viaf_id          | VIAF          | person/work        | "123456789"
```

### 2.4 Person / Contributor Identifiers

```text
Key              | Source        | Domain             | Example Value
-----------------|---------------|--------------------|------------------------
imdb_name_id     | IMDB          | person (actors, crew) | "nm0000102"
tmdb_person_id   | TMDB          | person             | "12345"
musicbrainz_artist_id | MusicBrainz | artist             | UUID
wikidata_id      | Wikidata      | person             | "Q12345"
viaf_id          | VIAF          | person             | "123456789"
orcid_id         | ORCID         | academic author    | "0000-0001-2345-6789"
isni             | ISNI          | person/organization| "0000000123456789"
discogs_artist_id| Discogs       | artist             | "12345"
```

### 2.5 Organization / Studio Identifiers

```text
Key              | Source        | Domain             | Example Value
-----------------|---------------|--------------------|------------------------
tmdb_network_id  | TMDB          | TV network         | "12345"
tmdb_company_id  | TMDB          | production company | "12345"
musicbrainz_label_id | MusicBrainz | record label      | UUID
discogs_label_id | Discogs       | record label       | "12345"
wikidata_id      | Wikidata      | organization       | "Q12345"
```

### 2.6 Composite Identifier Convention

Some media items merge data from multiple sources. The convention is to store
every source's ID under its own key. Do not derive or assume:

```json
{
  "imdb_id": "tt0111161",
  "tmdb_id": "278",
  "tvdb_id": "12345",
  "title": "The Shawshank Redemption",
  "year": "1994"
}
```

If a module needs to express that one ID is "primary," add a key:

```json
{
  "primary_id_source": "tmdb",
  "primary_id_value": "278"
}
```

### 2.7 Game Identifiers

```text
Key              | Source        | Domain             | Example Value
-----------------|---------------|--------------------|------------------------
steam_id         | Steam         | game               | "12345"
steam_app_id     | Steam         | game               | "12345"
gog_id           | GOG           | game               | "1234567890"
epic_id          | Epic Games    | game               | "ExampleGame-j5f3k"
nintendo_id      | Nintendo      | game               | "70010000000001"
psn_id           | PlayStation   | game               | "UP0001-CUSA00001_00-0000000000000001"
xbox_id          | Microsoft     | game               | "9P1234567890"
igdb_id          | IGDB          | game               | "12345"
giantbomb_id     | Giant Bomb    | game               | "3030-12345"
howlongtobeat_id | HowLongToBeat | game               | "12345"
mobygames_id     | MobyGames     | game               | "12345"
pcgamingwiki_id  | PCGamingWiki  | game               | "GameName"
```

### 2.8 Podcast / Serialized Audio Identifiers

```text
Key              | Source        | Domain             | Example Value
-----------------|---------------|--------------------|------------------------
itunes_id        | Apple Podcasts | podcast/show      | "1234567890"
spotify_id       | Spotify       | podcast show/ep   | "spotify:show:xxx"
castbox_id       | Castbox       | podcast show      | "12345"
overcast_id      | Overcast      | podcast show      | "12345"
pocketcasts_id   | Pocket Casts  | podcast show      | "12345"
podcastindex_id  | Podcast Index | podcast show      | "12345"
rss_feed_url     | RSS           | podcast show      | "https://feeds.example.com/serial"
episode_guid     | RSS           | podcast episode   | "abc123@podcast.com"
```

---

## 3. Core Media Metadata Schema

This section defines the canonical field names for media items. The schema is
organized as a flat key namespace -- all fields live at the top level of the
metadata map.

### 3.1 Common Fields (all media types)

```text
Key                  | Type     | Description                        | Example
---------------------|----------|------------------------------------|------------------------
title                | string   | Primary title                      | "The Shawshank Redemption"
original_title       | string   | Title in original language         | "Les Quatre Cents Coups"
sort_title           | string   | Title normalized for sorting       | "Shawshank Redemption, The"
year                 | string   | Release year                       | "1994"
release_date         | string   | ISO 8601 release date              | "1994-10-14"
overview             | string   | Plain-text synopsis/description    | "Two imprisoned men..."
tagline              | string   | Short marketing tagline            | "Fear can hold you..."
genres               | []string | Genre names                        | ["Drama"]
genre_ids            | []string | Internal genre identifiers         | ["genre_drama", "genre_crime"]
content_rating       | string   | Primary content rating             | "R"
content_ratings      | []struct | All known ratings                  | [{"system":"MPAA","rating":"R"},{"system":"FSK","rating":"16"}]
language             | string   | ISO 639-1 primary language         | "en"
original_language    | string   | ISO 639-1 original language        | "en"
languages            | []string | All available languages            | ["en","fr"]
countries            | []string | Production countries (ISO 3166-1)  | ["US"]
countries_of_origin  | []string | Origin countries                   | ["US"]
status               | string   | Release status                     | "released"
popularity           | string   | Provider-specific popularity score | "85.3"
vote_average         | string   | Average rating (0.0-10.0)         | "9.3"
vote_count           | string   | Total votes received               | "28742"
runtime_minutes      | string   | Runtime in minutes                 | "142"
poster_url           | string   | Primary poster/cover URL           | "https://..."
poster_urls          | []struct | Poster variants                    | [{"url":"...","language":"en","width":1000,"height":1500}]
backdrop_url         | string   | Primary backdrop/fanart URL        | "https://..."
backdrop_urls        | []struct | Backdrop variants                  | [{"url":"...","width":1920,"height":1080}]
logo_url             | string   | Primary logo URL                   | "https://..."
logo_urls            | []struct | Logo variants                      | [{"url":"...","language":"en"}]
thumb_url            | string   | Thumbnail URL                      | "https://..."
trailer_url          | string   | Trailer URL                        | "https://www.youtube.com/watch?v=..."
homepage_url         | string   | Official homepage                  | "https://www.example.com"
keywords             | []string | Search/filter keywords             | ["prison","friendship","hope"]
tags                 | []string | User-assigned tags                 | ["favorite","rewatch"]
```

### 3.2 Movie-Specific Fields

```text
Key                  | Type     | Description                        | Example
---------------------|----------|------------------------------------|------------------------
collection           | string   | Franchise or collection name       | "The Godfather Saga"
collection_id        | string   | External collection identifier     | "tmdb:12345"
collection_tmdb_id   | string   | TMDB collection ID                 | "12345"
budget               | string   | Production budget (USD)            | "25000000"
revenue              | string   | Box office revenue (USD)           | "73300000"
box_office           | string   | Box office revenue (USD)           | "73300000"
director             | string   | Primary director name              | "Frank Darabont"
directors            | []struct | All directors                      | [{"name":"Frank Darabont","tmdb_person_id":"123"}]
writers              | []struct | All writers                        | [{"name":"Stephen King"}]
cast                 | []struct | Top cast                           | [{"name":"Tim Robbins","character":"Andy Dufresne","order":0}]
awards               | string   | Awards summary                     | "Nominated for 7 Oscars"
festivals            | []string | Festival names                     | ["Cannes","Sundance"]
original_title       | string   | Title in original language         | (already in common)
release_dates        | []struct | Release dates by country           | [{"country":"US","date":"1994-10-14","type":"theatrical"}]
alternative_titles   | []struct | Alternative titles by country      | [{"country":"FR","title":"Les Évadés"}]
```

### 3.3 TV/Show-Specific Fields

```text
Key                  | Type     | Description                        | Example
---------------------|----------|------------------------------------|------------------------
show_type            | string   | Scripted, reality, talk_show, ...  | "scripted"
network              | string   | Primary network name               | "AMC"
networks             | []struct | All networks                       | [{"name":"AMC","logo_url":"..."}]
episode_count        | string   | Total episode count                | "62"
season_count         | string   | Total season count                 | "5"
episode_runtime_minutes | []string | Runtime per episode             | ["42","45"]
origin_country       | string   | Primary origin country             | "US"
origin_countries     | []string | All origin countries               | ["US","UK"]
seasons              | []struct | Season summary                     | [{"season_number":1,"episode_count":13,"air_date":"2008-01-20"}]
episodes             | []struct | Episode list                       | [{"episode_number":1,"season_number":1,"title":"Pilot","air_date":"2008-01-20"}]
airs_day             | string   | Day of week aired                  | "Sunday"
airs_time            | string   | Time of day aired                  | "21:00"
airs_timezone        | string   | Timezone                           | "America/New_York"
next_air_date        | string   | ISO 8601 next episode air date     | "2026-01-15"
last_air_date        | string   | ISO 8601 last episode air date     | "2025-12-20"
created_by           | string   | Creator name                       | "Vince Gilligan"
created_by           | []struct | Creator array                      | [{"name":"Vince Gilligan"}]
production_companies | []struct | Production companies               | [{"name":"Sony Pictures Television"}]
content_type         | string   | "movie" or "tv"                    | "tv"
```

### 3.4 Episode-Specific Fields

```text
Key                  | Type     | Description                        | Example
---------------------|----------|------------------------------------|------------------------
episode_number       | string   | Episode number within season       | "1"
season_number        | string   | Season number                      | "1"
absolute_number      | string   | Absolute episode number (anime)    | "101"
dvd_episode_number   | string   | Episode number on DVD              | "1"
dvd_season_number    | string   | Season number on DVD               | "1"
air_date             | string   | ISO 8601 original air date         | "2008-01-20"
still_url            | string   | Episode screenshot URL             | "https://..."
still_urls           | []struct | Screenshot variants                | [{"url":"...","width":1920,"height":1080}]
director             | string   | Episode director                   | "Vince Gilligan"
writer               | string   | Episode writer                     | "Vince Gilligan"
guest_stars          | []struct | Guest cast                         | [{"name":"..."}]
production_code      | string   | Production code                    | "101"
```

### 3.5 Music-Specific Fields

```text
Key                  | Type     | Description                        | Example
---------------------|----------|------------------------------------|------------------------
artist               | string   | Primary artist name                | "Radiohead"
artists              | []struct | All artists/contributors           | [{"name":"Radiohead","role":"main"}]
album_artist         | string   | Album artist (compilations)        | "Various Artists"
album_artists        | []struct | Album artist objects               | [{"name":"Various Artists"}]
album                | string   | Album name                         | "OK Computer"
album_id             | string   | Internal or external album ID      | "mbid:..."
release_type         | string   | album, single, ep, compilation     | "album"
track_number         | string   | Track number on album              | "3"
disc_number          | string   | Disc number (multi-disc)           | "1"
total_tracks         | string   | Total tracks on release            | "12"
total_discs          | string   | Total discs in release             | "2"
duration_ms          | string   | Track duration in milliseconds     | "258000"
duration_seconds     | string   | Track duration in seconds          | "258"
label                | string   | Primary record label               | "Parlophone"
labels               | []struct | All labels                         | [{"name":"Parlophone","catalog_number":"7243 8 55229 2 5"}]
barcode              | string   | UPC/EAN barcode                    | "724385522925"
format               | string   | Physical/digital format            | "CD"
is_compilation       | string   | Compilation flag                   | "false"
is_live              | string   | Live recording flag                | "false"
is_remaster          | string   | Remaster flag                      | "true"
recording_date       | string   | ISO 8601 recording date            | "1996-07-01"
release_date         | string   | ISO 8601 release date (already in common) | "1997-05-21"
cover_url            | string   | Album cover URL                    | "https://..."
cover_urls           | []struct | Cover art variants                 | [{"url":"...","width":300,"height":300}]
mbid                 | string   | MusicBrainz ID (shorthand)         | UUID
artist_mbid          | string   | MusicBrainz artist ID              | UUID
album_mbid           | string   | MusicBrainz release group ID       | UUID
track_mbid           | string   | MusicBrainz recording ID           | UUID
```

### 3.6 Book-Specific Fields

```text
Key                  | Type     | Description                        | Example
---------------------|----------|------------------------------------|------------------------
author               | string   | Primary author name                | "George Orwell"
authors              | []struct | All authors                        | [{"name":"George Orwell","role":"author"}]
publisher            | string   | Publisher name                     | "Secker & Warburg"
published_date       | string   | ISO 8601 publication date          | "1949-06-08"
page_count           | string   | Number of pages                    | "328"
series               | string   | Series name                        | "Harry Potter"
series_position      | string   | Position in series                 | "1"
series_total         | string   | Total books in series              | "7"
edition              | string   | Edition name                       | "First Edition"
format               | string   | Book format                        | "hardcover"
language             | string   | ISO 639-1 (already in common)      | "en"
cover_url            | string   | Book cover URL                     | "https://..."
cover_urls           | []struct | Cover variants                     | [{"url":"..."}]
subtitle             | string   | Book subtitle                      | "A Novel"
contributors         | []struct | Other contributors                 | [{"name":"...","role":"translator"}]
subjects             | []string | Subject headings                   | ["Dystopian fiction","Totalitarianism"]
awards               | string   | Awards text                        | "Prometheus Award Hall of Fame"
excerpt              | string   | Short excerpt / first lines        | "It was a bright cold day in April..."
abridged             | string   | Is abridged                        | "false"
audiobook_narrator   | string   | Narrator for audiobooks            | "Simon Prebble"
audiobook_runtime    | string   | Audiobook length (minutes)         | "465"
```

### 3.7 Person / Contributor Object Structure

When representing a person (actor, director, artist, author, etc.) in an array
of objects, the canonical structure is:

```json
{
  "name": "Tim Robbins",
  "role": "Actor",
  "character": "Andy Dufresne",
  "order": 0,
  "image_url": "https://...",
  "imdb_name_id": "nm0000209",
  "tmdb_person_id": "12345"
}
```

```text
Key                 | Type     | Description                        | Required?
--------------------|----------|------------------------------------|----------
name                | string   | Full display name                  | yes
role                | string   | Job title (actor, director, etc.)  | no
character           | string   | Character portrayed (actors only)  | no
order               | string   | Billing/cast order (0-based)       | no
image_url           | string   | Headshot/profile URL               | no
imdb_name_id        | string   | IMDB person ID                     | no
tmdb_person_id      | string   | TMDB person ID                     | no
musicbrainz_artist_id | string | MusicBrainz artist ID              | no
```

### 3.8 Artwork Variant Object Structure

```json
{
  "url": "https://image.tmdb.org/t/p/w500/example.jpg",
  "language": "en",
  "width": 500,
  "height": 750,
  "aspect_ratio": 0.667,
  "type": "poster",
  "is_primary": true
}
```

```text
Key                 | Type     | Description                        | Required?
--------------------|----------|------------------------------------|----------
url                 | string   | Full URL to the image              | yes
language            | string   | ISO 639-1 language code            | no
width               | string   | Pixel width                        | no
height              | string   | Pixel height                       | no
aspect_ratio        | string   | Aspect ratio (w/h)                 | no
type                | string   | Artwork type hint                  | no
is_primary          | string   | "true" if primary variant          | no
```

### 3.9 Content Rating Object Structure

```json
{
  "system": "MPAA",
  "rating": "R",
  "reason": "Language, violence",
  "country": "US"
}
```

### 3.10 Video Game-Specific Fields

```text
Key                     | Type     | Description                        | Example
------------------------|----------|------------------------------------|------------------------
platform                | string   | Primary target platform            | "playstation-5"
platforms               | []string | All supported platforms            | ["playstation-5","xbox-series-x","pc"]
platform_id             | string   | Platform identifier (internal)     | "ps5_01"
developer               | string   | Primary developer name             | "Rocksteady Studios"
developers              | []struct | All developers                     | [{"name":"Rocksteady Studios"}]
publisher               | string   | Primary publisher name             | "Warner Bros. Interactive"
publishers              | []struct | All publishers                     | [{"name":"Warner Bros. Interactive"}]
game_mode               | string   | Single-player, multiplayer, etc.   | "single-player"
game_modes              | []string | All supported modes                | ["single-player","multiplayer"]
player_count_min        | string   | Minimum players                    | "1"
player_count_max        | string   | Maximum players                    | "8"
esrb_rating             | string   | ESRB rating                        | "M"
pegi_rating             | string   | PEGI rating                        | "18"
usk_rating              | string   | USK rating                         | "16"
gesamt_rating           | string   | German FSK rating                  | "16"
steam_id                | string   | Steam app ID                       | "12345"
steam_app_id            | string   | Steam app ID (alias)               | "12345"
gog_id                  | string   | GOG game ID                        | "1234567890"
epic_id                 | string   | Epic Games Store ID                | "ExampleGame-j5f3k"
nintendo_id             | string   | Nintendo eShop ID                  | "70010000000001"
psn_id                  | string   | PlayStation Network ID             | "UP0001-CUSA00001_00-0000000000000001"
xbox_id                 | string   | Xbox product ID                    | "9P1234567890"
igdb_id                 | string   | IGDB game ID                       | "12345"
giantbomb_id            | string   | Giant Bomb game ID                 | "3030-12345"
howlongtobeat_id        | string   | HowLongToBeat ID                   | "12345"
metacritic_score        | string   | Metacritic score (0-100)           | "91"
opencritic_score        | string   | OpenCritic score (0-100)           | "89"
ign_score               | string   | IGN score (0-10)                   | "9.2"
steam_review_score      | string   | Steam review rating (%)            | "96"
steam_review_count      | string   | Steam review count                 | "250000"
dlc                     | []struct | DLC / expansions                   | [{"name":"The Frozen Wilds","steam_id":"54321"}]
expansion_for           | string   | Parent game this is DLC for        | "game-id-123"
series                  | string   | Game franchise name                | "Batman: Arkham"
series_position         | string   | Position in franchise              | "3"
franchise               | string   | IP / franchise name                | "Batman"
```

Platform names should use the convention (hyphenated lowercase, with `-` between
manufacturer and model). This covers everything from retro to modern:

```text
Home Computers & PC
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Windows PC                  | pc
  Mac                         | mac
  Linux                       | linux
  Commodore 64                | commodore-64
  Commodore 128               | commodore-128
  Amiga                       | amiga-500, amiga-1200
  Atari ST                    | atari-st
  Atari 8-bit                 | atari-800
  ZX Spectrum                 | zx-spectrum
  Amstrad CPC                 | amstrad-cpc
  MSX                         | msx
  MSX2                        | msx2
  PC-88                       | pc-88
  PC-98                       | pc-98
  X68000                      | x68000
  FM Towns                    | fm-towns
  Mobile / Tablet             | mobile
  Browser / Web               | web

Nintendo
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Nintendo Entertainment System| nintendo-nes
  Famicom                     | famicom
  Famicom Disk System         | famicom-disk-system
  Super Nintendo              | nintendo-snes
  Super Famicom               | super-famicom
  Nintendo 64                 | nintendo-64
  Nintendo 64 DD              | nintendo-64dd
  Nintendo GameCube           | nintendo-gamecube
  Wii                         | nintendo-wii
  Wii U                       | nintendo-wii-u
  Nintendo Switch             | nintendo-switch
  Nintendo Switch 2           | nintendo-switch-2
  Game Boy                    | game-boy
  Game Boy Color              | game-boy-color
  Game Boy Advance            | game-boy-advance
  Nintendo DS                 | nintendo-ds
  Nintendo 3DS                | nintendo-3ds
  Virtual Boy                 | virtual-boy
  Game & Watch                | game-and-watch
  Pokemon Mini                | pokemon-mini
  Satellaview                 | satellaview
  Super Game Boy              | super-game-boy
  Game Boy Player             | game-boy-player

Sega
  Platform                    | Canonical name
  ----------------------------|------------------------------
  SG-1000                     | sg-1000
  Sega Master System          | sega-master-system
  Sega Genesis / Mega Drive   | sega-genesis
  Sega CD / Mega CD           | sega-cd
  Sega 32X                    | sega-32x
  Sega Saturn                 | sega-saturn
  Sega Dreamcast              | sega-dreamcast
  Sega Game Gear              | sega-game-gear
  Sega Nomad                  | sega-nomad
  Sega Pico                   | sega-pico
  Sega Naomi                  | sega-naomi
  Sega Naomi 2                | sega-naomi-2

Sony
  Platform                    | Canonical name
  ----------------------------|------------------------------
  PlayStation                 | playstation
  PlayStation 2               | playstation-2
  PlayStation 3               | playstation-3
  PlayStation 4               | playstation-4
  PlayStation 5               | playstation-5
  PSP                         | psp
  PS Vita                     | playstation-vita
  PocketStation               | pocketstation

Microsoft
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Xbox                        | xbox
  Xbox 360                    | xbox-360
  Xbox One                    | xbox-one
  Xbox Series X|S             | xbox-series-x

Atari
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Atari 2600                  | atari-2600
  Atari 5200                  | atari-5200
  Atari 7800                  | atari-7800
  Atari Jaguar                | atari-jaguar
  Atari Jaguar CD             | atari-jaguar-cd
  Atari Lynx                  | atari-lynx
  Atari ST                    | atari-st
  Atari 8-bit family          | atari-400, atari-800
  Atari Flashback             | atari-flashback

SNK
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Neo Geo AES                 | neo-geo-aes
  Neo Geo MVS                 | neo-geo-mvs
  Neo Geo CD                  | neo-geo-cd
  Neo Geo Pocket              | neo-geo-pocket
  Neo Geo Pocket Color        | neo-geo-pocket-color
  SNK vs. Capcom (arcade)     | snk-arcade-hardware

NEC / Hudson Soft
  Platform                    | Canonical name
  ----------------------------|------------------------------
  PC Engine / TurboGrafx-16   | pc-engine
  PC Engine CD / TurboGrafx-CD| pc-engine-cd
  PC Engine SuperGrafx        | supergrafx
  PC-FX                       | pc-fx

Other Consoles & Handhelds
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Magnavox Odyssey            | magnavox-odyssey
  Magnavox Odyssey²           | magnavox-odyssey-2
  Intellivision               | intellivision
  ColecoVision                | coleco-vision
  Fairchild Channel F         | fairchild-channel-f
  Bally Astrocade             | bally-astrocade
  Vectrex                     | vectrex
  Philips CD-i                | philips-cd-i
  Tapwave Zodiac              | tapwave-zodiac
  N-Gage                      | n-gage
  WonderSwan                  | wonderswan
  WonderSwan Color            | wonderswan-color
  SwanCrystal                 | swancrystal
  GP32                        | gp32
  GP2X                        | gp2x
  Pandora                     | pandora
  GPD Win                     | gpd-win
  GPD Win 2                   | gpd-win-2
  GPD Win 3                   | gpd-win-3
  GPD Win 4                   | gpd-win-4
  Steam Deck                  | steam-deck
  AYN Odin                    | ayn-odin
  Retro handheld (emulation)  | retro-handheld

Arcade
  Platform                    | Canonical name
  ----------------------------|------------------------------
  Arcade (generic)            | arcade
  MAME                        | mame
  CPS-1 (Capcom)              | cps-1
  CPS-2 (Capcom)              | cps-2
  CPS-3 (Capcom)              | cps-3
  Neo Geo MVS                 | neo-geo-mvs
  Sega Naomi                  | sega-naomi
  Sega Naomi 2                | sega-naomi-2
  Sega Model 2                | sega-model-2
  Sega Model 3                | sega-model-3
  Namco System 1              | namco-system-1
  Namco System 2              | namco-system-2
  Taito F3                    | taito-f3
  Taito X                     | taito-x
  Atomiswave                  | atomiswave
  PlayStation 2 arcade hw     | ps2-arcade-hw
  Chihiro (Xbox-based)        | chihiro
  Triforce (GC-based)         | triforce
  Lindbergh (PC-based)        | lindbergh

VR / Emerging
  Platform                    | Canonical name
  ----------------------------|------------------------------
  VR (generic)                | vr
  PlayStation VR              | playstation-vr
  PlayStation VR2             | playstation-vr2
  Meta Quest                  | meta-quest
  HTC Vive                    | htc-vive
  Valve Index                 | valve-index
  Apple Vision Pro            | apple-vision-pro
```

### 3.11 Podcast & Serialized Audio Fields

```text
Key                     | Type     | Description                        | Example
------------------------|----------|------------------------------------|------------------------
podcast_name            | string   | Podcast series name                | "Serial"
podcast_id              | string   | Internal podcast identifier        | "podcast:serial"
episode_type            | string   | full, trailer, bonus               | "full"
episode_guid            | string   | Permanent episode GUID             | "abc123@podcast.com"
duration_seconds        | string   | Episode duration                   | "2517"
explicit                | string   | Explicit content flag              | "true"
host                    | string   | Primary host name                  | "Sarah Koenig"
hosts                   | []struct | All hosts/panelists                | [{"name":"Sarah Koenig","role":"host"}]
rss_feed_url            | string   | RSS feed URL for series            | "https://feeds.example.com/serial"
website_url             | string   | Episode show notes URL             | "https://..."
season_number           | string   | Podcast season                     | "1"
episode_number          | string   | Episode within season              | "3"
itunes_id               | string   | Apple Podcasts ID                  | "1234567890"
spotify_id              | string   | Spotify show/episode ID            | "spotify:show:xxx"
castbox_id              | string   | Castbox ID                         | "12345"
overcast_id             | string   | Overcast ID                        | "12345"
pocketcasts_id          | string   | Pocket Casts ID                    | "12345"
podcastindex_id         | string   | Podcast Index ID                   | "12345"
```

### 3.12 Cross-Media Relationship Fields

Some media items are connected to others -- sequels, adaptations, spin-offs,
novelizations, crossovers. These fields express those links:

```text
Key                     | Type       | Description                        | Example
------------------------|------------|------------------------------------|------------------------
franchise_name          | string     | Franchise / IP name                | "Star Wars"
franchise_position      | string     | Order in franchise                 | "4"
franchise_total         | string     | Total items in franchise           | "9"
collection              | string     | Collection / set name              | "The Before Trilogy"
collection_id           | string     | Collection identifier              | "tmdb:123"
belongs_to_collection   | string     | Same as collection                 | "The Godfather Saga"
related_items           | []struct   | Linked media items                 | [{"type":"sequel","tmdb_id":"680"}]
remake_of               | string     | Foreign/game remake               | "tmdb:123"
based_on                | string     | Source material                    | "isbn:978-0-00-000000-0"
inspired_by             | string     | Work that inspired this            | "imdb:tt1234567"
adapted_from            | string     | Same as based_on                   | "isbn:978-0-00-000000-0"
original_version        | string     | For remakes, the original          | "imdb:tt1234567"
spin_off_from           | string     | Parent media this spun off from    | "tmdb:456"
cross_over_with         | []string   | Crossover event participants       | ["tmdb:789","tmdb:101"]
featured_in             | []string   | Compilation / anthology entries    | ["collection:xyz"]
featured_at             | []struct   | Festival or event showings         | [{"event":"Sundance","year":"2023"}]
references              | []string   | Works referenced in this media     | ["imdb:tt1111111"]
parody_of               | string     | Work being parodied                | "imdb:tt2222222"
```

Related item entry structure:

```json
{
  "type": "sequel",
  "title": "The Empire Strikes Back",
  "imdb_id": "tt0080684",
  "tmdb_id": "1891",
  "year": "1980",
  "release_date": "1980-05-21"
}
```

```text
Key                     | Type     | Description                        | Required?
------------------------|----------|------------------------------------|----------
type                    | string   | Relationship type (see below)      | yes
title                   | string   | Display title of related item      | no
imdb_id                 | string   | IMDB identifier                    | no
tmdb_id                 | string   | TMDB identifier                    | no
year                    | string   | Release year                       | no
release_date            | string   | ISO 8601 release date              | no
```

Standard relationship types:

```text
Type                | Meaning
--------------------|----------------------------------------
sequel              | Direct sequel (story continues)
prequel             | Story set before this work
remake              | Same story remade
reboot              | Franchise restarted
spin_off            | Character/setting spun into own work
crossover           | Characters/worlds cross over
adaptation          | Adapted from another medium (book to film)
novelization        | Novel based on film/game
based_on_true_story | Inspired by real events
feature_in          | Appears in a compilation/anthology
reference           | Referenced or mentioned in this work
parody              | Parody/spoof of another work
parent              | Parent item (episode in a show, DLC for a game)
child               | Child item (episode, DLC, spin-off)
same_universe       | Shares a fictional universe
```

### 3.13 Technical & Container Metadata

For files that carry technical encoding information (after mediainfo scanning,
transcoding, or download analysis):

```text
Key                     | Type     | Description                        | Example
------------------------|----------|------------------------------------|------------------------
container               | string   | Container format                   | "mkv"
format                  | string   | Human-readable format              | "Matroska"
codec                   | string   | Primary video codec                | "h265"
codec_name              | string   | Full codec name                    | "HEVC"
codec_profile           | string   | Codec profile                      | "Main 10"
codec_level             | string   | Codec level                        | "5.1"
resolution              | string   | Resolution string                   | "3840x2160"
width                   | string   | Pixel width                        | "3840"
height                  | string   | Pixel height                       | "2160"
aspect_ratio            | string   | Display aspect ratio               | "16:9"
pixel_aspect_ratio      | string   | Pixel aspect ratio                 | "1:1"
frame_rate              | string   | Frame rate                         | "23.976"
bitrate                 | string   | Video bitrate (kbps)               | "15000"
bitrate_mode            | string   | VBR, CBR, etc.                     | "VBR"
bit_depth               | string   | Color bit depth                    | "10"
color_space             | string   | Color space                        | "YUV"
color_primaries         | string   | Color primaries                    | "BT.2020"
color_transfer          | string   | Transfer characteristics           | "PQ"
hdr_format              | string   | HDR format string                  | "HDR10"
hdr_format_compat       | []string | Compatible HDR formats             | ["HDR10","Dolby Vision"]
hdr_dv_version          | string   | Dolby Vision version               | "1.0"
hdr_dv_profile          | string   | Dolby Vision profile               | "8.1"
hdr10plus               | string   | HDR10+ available                   | "true"
audio_codec             | string   | Primary audio codec                | "dts"
audio_codec_name        | string   | Full audio codec name              | "DTS-HD Master Audio"
audio_channels          | string   | Audio channel count                | "7.1"
audio_channel_layout    | string   | Channel layout string              | "L R C LFE Ls Rs Lb Rb"
audio_sample_rate       | string   | Audio sample rate (Hz)             | "48000"
audio_bit_depth         | string   | Audio bit depth                    | "24"
audio_bitrate           | string   | Audio bitrate (kbps)               | "1536"
audio_language          | string   | ISO 639-1 audio language           | "en"
audio_languages         | []string | All audio languages                | ["en","fr","ja"]
subtitle_languages      | []string | All subtitle languages             | ["en","fr","de"]
subtitle_formats        | []string | Subtitle format types              | ["srt","pgs","vtt"]
has_forced_subtitles    | string   | Has forced subtitle tracks         | "true"
has_sdh_subtitles       | string   | Has SDH subtitle tracks            | "false"
chapters_count          | string   | Number of chapters                 | "12"
chapters                | []struct | Chapter markers                    | [{"name":"Opening","time_ms":"0"},{"name":"Chapter 1","time_ms":"300000"}]
size_bytes              | string   | File size in bytes                 | "8589934592"
duration_ms             | string   | Duration in milliseconds           | "8520000"
duration_seconds        | string   | Duration in seconds                | "8520"
is_interlaced           | string   | Interlaced content flag            | "false"
is_3d                   | string   | Stereoscopic 3D content flag       | "false"
3d_format               | string   | 3D format type                     | "side-by-side"
multi_angle             | string   | Multi-angle content                | "false"
region_code             | string   | DVD/Blu-ray region code            | "1"
disc_number             | string   | Disc number (multi-disc sets)      | "1"
total_discs             | string   | Total discs in set                 | "3"
```

### 3.14 Metadata Provenance Fields

These fields track where metadata came from, enabling downstream modules to
make trust decisions:

```text
Key                     | Type     | Description                        | Example
------------------------|----------|------------------------------------|------------------------
metadata_source         | string   | Module ID that populated this      | "metadata-tmdb"
metadata_source_version | string   | Version of the source module       | "1.2.3"
metadata_timestamp      | string   | ISO 8601 when metadata was fetched | "2026-06-10T12:00:00Z"
metadata_confidence     | string   | Confidence score 0.0-1.0           | "0.95"
metadata_ttl_seconds    | string   | Seconds before metadata is stale   | "86400"
metadata_etag           | string   | Provider ETag for cache validation | "\"abc123\""
```

When multiple modules enrich the same item, the convention is to keep the
last writer's provenance on the top-level keys and use per-field tracking
inside a nested object if needed:

```json
{
  "title": "The Shawshank Redemption",
  "imdb_id": "tt0111161",
  "poster_url": "https://...",
  "metadata_source": "metadata-tmdb",
  "metadata_timestamp": "2026-06-10T12:00:00Z",
  "genres": ["Drama", "Crime"],
  "user_rating": "9.5",
  "metadata_provenance": {
    "title": { "source": "metadata-tmdb", "timestamp": "2026-06-10T12:00:00Z" },
    "imdb_id": { "source": "metadata-tmdb", "timestamp": "2026-06-10T12:00:00Z" },
    "poster_url": { "source": "artwork-tmdb", "timestamp": "2026-06-10T12:05:00Z" },
    "genres": { "source": "metadata-tmdb", "timestamp": "2026-06-10T12:00:00Z" },
    "user_rating": { "source": "media-manager", "timestamp": "2026-06-10T14:00:00Z" }
  }
}
```

This level of detail is optional but recommended for modules that enrich
existing metadata rather than creating it fresh.

---

## 4. Event Type Conventions

### 4.1 Domain Namespaces

Official modules use domain namespaced event types. The namespace is the
module's role or the media type domain:

```text
Pattern:           <domain>.<action>[.<detail>]
Examples:
  media.movie.requested
  media.movie.imported
  media.movie.deleted
  media.tv.requested
  media.episode.imported
  media.music.requested
  media.book.requested
  download.torrent.started
  download.torrent.completed
  download.usenet.started
  metadata.movie.enriched
  indexer.movie.found
  transcode.video.started
  transcode.video.completed
  transcode.video.failed
  notification.sent
  artwork.fetched
  importlist.synced
  workflow.tapestry.started
```

### 4.2 Standard Actions

When defining event types for your module, use these standard action verbs:

```text
Action       | Meaning                          | Example
-------------|----------------------------------|-------------------------------
requested    | User requested an item           | media.movie.requested
imported     | Item added to library            | media.movie.imported
found        | Item discovered by indexer       | indexer.movie.found
enriched     | Metadata updated/enriched        | metadata.movie.enriched
started      | Process began                    | download.movie.started
progress     | Process progress update          | download.movie.progress
completed    | Process finished successfully    | transcode.video.completed
failed       | Process failed                   | transcode.video.failed
cancelled    | Process cancelled by user        | download.movie.cancelled
deleted      | Item removed from library        | media.movie.deleted
updated      | Item metadata updated            | media.movie.updated
synced       | Synchronization complete         | importlist.synced
fetched      | External data retrieved          | artwork.movie.fetched
```

### 4.3 Request/Reply Pattern

For request-response interactions via the event bus, use `.reply` suffix:

```text
Request:  media.movie.lookup
Reply:    media.movie.lookup.reply
```

The reply event should include a correlation mechanism. The `Event.Metadata`
map is the right place for correlation IDs. Use these keys:

```text
Metadata Key       | Description
-------------------|--------------------------------------
correlation_id     | UUID linking reply to request
request_event_id   | The Event.ID of the original request
request_source     | The calling module's ID
request_type       | The original event type (echoed)
```

### 4.4 Domain-Specific Events (Official Modules)

These event types are reserved by official module implementations. Module
authors should check here before defining new event types.

**Media domain:** (`media.*`)
```text
media.movie.requested       # A user wants a movie
media.movie.lookup          # Look up movie metadata
media.movie.lookup.reply    # Movie metadata lookup result
media.movie.imported        # Movie imported to library
media.movie.updated         # Movie metadata updated
media.movie.deleted         # Movie removed from library
media.tv.requested          # A user wants a TV show
media.tv.lookup             # Look up TV show metadata
media.tv.lookup.reply       # TV show metadata lookup result
media.tv.imported           # TV show imported to library
media.season.imported       # Season imported
media.episode.imported      # Episode imported
media.music.requested       # A user wants music
media.music.imported        # Music imported to library
media.book.requested        # A user wants a book
media.book.imported         # Book imported to library
media.book.lookup           # Look up book metadata
media.book.lookup.reply     # Book metadata lookup result
media.file.discovered       # New file found in watched folder
media.file.deleted          # File deleted from monitored path
```

**Download domain:** (`download.*`)
```text
download.movie.requested    # Enqueue movie for download
download.movie.started      # Download started
download.movie.progress     # Download progress update
download.movie.completed    # Download finished
download.movie.failed       # Download failed
download.movie.cancelled    # Download cancelled
download.tv.requested       # Enqueue TV episode for download
download.tv.started         # Download started
download.tv.completed       # Download completed
download.tv.failed          # Download failed
download.music.requested    # Enqueue music for download
download.music.completed    # Music downloaded
download.book.requested     # Enqueue book for download
download.book.completed     # Book downloaded
```

**Metadata domain:** (`metadata.*`)
```text
metadata.movie.enriched     # Movie metadata refreshed
metadata.tv.enriched        # TV show metadata refreshed
metadata.music.enriched     # Music metadata refreshed
metadata.book.enriched      # Book metadata refreshed
metadata.movie.lookedup     # Raw metadata lookup result
metadata.tv.lookedup        # Raw TV metadata result
metadata.person.lookedup    # Person metadata result
```

**Transcode domain:** (`transcode.*`)
```text
transcode.video.requested   # Enqueue video transcode
transcode.video.started     # Transcode started
transcode.video.progress    # Transcode progress (%)
transcode.video.completed   # Transcode finished
transcode.video.failed      # Transcode failed
transcode.audio.requested   # Enqueue audio transcode
transcode.audio.started     # Audio transcode started
transcode.audio.completed   # Audio transcode completed
transcode.audio.failed      # Audio transcode failed
transcode.extract.started   # Subtitle/attachment extraction
transcode.extract.completed # Extraction completed
```

**Indexer domain:** (`indexer.*`)
```text
indexer.movie.found         # Indexer found a movie result
indexer.tv.found            # Indexer found a TV result
indexer.music.found         # Indexer found a music result
indexer.book.found          # Indexer found a book result
indexer.search.started      # Indexer search initiated
indexer.search.failed       # Indexer search failed
indexer.search.completed    # Indexer search completed
```

**Game domain:** (`media.game.*`)
```text
media.game.requested        # A user wants a game
media.game.lookup           # Look up game metadata
media.game.lookup.reply     # Game metadata lookup result
media.game.imported         # Game imported to library
media.game.updated          # Game metadata updated
download.game.requested     # Enqueue game for download
download.game.started       # Game download started
download.game.completed     # Game download completed
download.game.failed        # Game download failed
metadata.game.enriched      # Game metadata refreshed
indexer.game.found          # Indexer found a game result
transcode.game.extract      # Game resource extraction
```

**Podcast domain:** (`media.podcast.*`)
```text
media.podcast.requested     # A user wants a podcast
media.podcast.lookup        # Look up podcast metadata
media.podcast.lookup.reply  # Podcast metadata result
media.podcast.subscribed    # Podcast subscribed
media.podcast.imported      # Episode imported to library
media.podcast.episode.downloaded # Episode file downloaded
media.podcast.updated       # Podcast metadata refreshed
media.podcast.new_episode   # New episode discovered
download.podcast.started    # Podcast download started
download.podcast.completed  # Podcast download completed
metadata.podcast.enriched   # Podcast metadata refreshed
```

---

## 5. Module-to-Module Communication

### 5.1 Event Bus Communication

Modules communicate primarily through the event bus. The conventions:

1. **Publish fire-and-forget events** for notifications of state changes
   (e.g., `media.movie.imported`).
2. **Use Request/Reply** for queries that need a response
   (e.g., `media.movie.lookup` / `media.movie.lookup.reply`).
3. **Set `Source` to the module's own ID** on every published event.
4. **Set `TraceID`** if you have one (propagates from the original request).
5. **Use `Metadata` for correlation** in request/reply patterns.
6. **Do not rely on delivery order.** Events are eventually delivered.

### 5.2 gRPC Mesh Calls

For direct module-to-module calls through the gRPC mesh, use method names that
reflect the action being performed:

```text
Pattern:    <verb><Noun>
Examples:
  LookupMovie
  LookupTVShow
  StartDownload
  CancelDownload
  GetProgress
  TranscodeMedia
  SearchIndexer
  GetMetadata
  EnqueueWorkflow
  CheckHealth
```

Method names should be `PascalCase`, action-oriented, and specific to the
domain contract. Each contract repo (e.g., `contracts-metadata`) should define
its own set of methods.

### 5.3 Calling Convention

When calling another module via the gRPC mesh:

1. **Caller** should handle timeouts and retries locally.
2. **Callee** should return meaningful errors (see Section 8).
3. **Payload** should be JSON-encoded protobuf, or just JSON.
4. **Headers** in `CallRequest` should include:
   - `x-module-id` — the caller's module ID
   - `x-trace-id` — propagated trace ID
   - `x-request-id` — unique request ID for idempotency
   - `x-timeout-ms` — caller's timeout hint

### 5.4 Metadata Exchange Patterns

When passing media metadata between modules (e.g., indexer → downloader →
importer), the receiving module should:

1. **Preserve all keys** from the previous stage. Add, never remove.
2. **Prefix custom keys** with `module_name_` if they're not in the standard
   schema.
3. **Set `content_type`** to `"movie"`, `"tv"`, `"episode"`, `"music"`,
   `"book"`, or `"other"` so downstream modules know which schema applies.
4. **Set `media_id`** to a stable, unique identifier for the media item within
   the local library.

The `content_type` field is the primary dispatch key. A downloader module
emits `download.movie.completed` with `content_type: "movie"`, and anything
subscribing to `media.movie.*` events knows the metadata map uses the movie
schema.

### 5.5 Storage Metadata Conventions

When storing files via the storage orchestrator, modules should set
`ObjectInfo.Metadata` keys:

```text
Key                   | Description                        | Example
----------------------|------------------------------------|------------------------
content_type          | Media type                        | "movie"
media_id              | Stable library item ID            | "movie:12345"
module_id             | Module that stored this           | "downloader-qbittorrent"
source_url            | Original download URL             | "https://..."
source_filename       | Original filename                 | "movie.mkv"
import_date           | ISO 8601 date of import           | "2026-06-10T12:00:00Z"
quality               | Quality tag                       | "2160p"
format                | Container format                  | "mkv"
codec                 | Video codec                       | "h265"
audio_codec           | Audio codec                       | "dts"
resolution            | Resolution string                 | "3840x2160"
bitrate               | Bitrate in kbps                   | "15000"
size_bytes            | File size in bytes                | "8589934592"
checksum_sha256       | SHA-256 checksum                  | hex string
release_group         | Release group name                | "TERMiNAL"
season_number         | For TV episodes                   | "1"
episode_number        | For TV episodes                   | "3"
tmdb_id               | TMDB identifier of the content    | "12345"
imdb_id               | IMDB identifier                   | "tt0111161"
original_title        | Original release title            | "Movie Name"
year                  | Release year                      | "2025"
```

---

## 6. Configuration Standards

### 6.1 Setting Naming

Modules that expose settings via `SettingsProvider` should name their settings
using `snake_case`:

```text
download_path
max_connections
enable_notifications
api_key              # Avoid naming sensitive fields "password" or "secret"
quality_profile      # Use descriptive nouns
```

### 6.2 Common Setting Names

For consistency across official modules, use these setting names when the
function is equivalent:

```text
Setting Name                | Type      | Default       | Description
----------------------------|-----------|---------------|------------------------------
enabled                     | bool      | true          | Master toggle for the module
host                        | string    | "localhost"   | Remote service hostname
port                        | int       | 8080          | Remote service port
base_url                    | string    | ""            | Base URL for remote API
api_key                     | string    | ""            | API key/token for remote service
username                    | string    | ""            | Authentication username
password                    | string    | ""            | Authentication password
timeout_seconds             | int       | 30            | HTTP/gRPC timeout
retry_count                 | int       | 3             | Retry attempts on failure
retry_delay_seconds         | int       | 5             | Delay between retries
max_concurrent              | int       | 1             | Max concurrent operations
rate_limit_per_minute       | int       | 60            | API rate limit
log_level                   | string    | "info"        | Module-specific log level
download_path               | string    | ""            | File download destination
watch_paths                 | []string  | []            | Paths to monitor for changes
quality_profile             | string    | "default"     | Quality preference profile
language_profile            | string    | "en"          | Language preference
content_types               | []string  | ["movie","tv"]| Media types to handle
enable_notifications        | bool      | true          | Toggle notifications
enable_automatic_search     | bool      | false         | Auto-search on request
enable_automatic_upgrades   | bool      | false         | Auto-upgrade quality
schedule_cron               | string    | ""            | Cron expression for scheduled tasks
```

### 6.3 Sensitive Settings

Settings that contain secrets (api_key, password, token) should:

1. Use `"password"` input type in the settings definition (for UI masking).
2. Be stored securely by core's `SecretsProvider`.
3. Never appear in logs or audit records (core already handles this for
   secret-backed settings).

---

## 7. Role and Capability Conventions

### 7.1 Standard Roles

Official modules use one or more of these roles:

```text
Role               | Description
-------------------|----------------------------------------------
downloader         | Downloads media from remote sources
indexer            | Searches for media availability
metadata           | Fetches metadata from external providers
media_manager      | Manages library organization and lifecycle
playback           | Serves media for streaming
transcoder         | Transcodes media formats
artwork            | Fetches posters, backdrops, fanart
quality            | Makes quality/upgrade decisions
mediainfo          | Extracts technical media information
resolver           | Resolves identifiers across providers
notification       | Sends notifications (Discord, Slack, etc.)
importlist         | Imports media from lists/watchlists
tag                | Auto-tags media with labels
filewatcher        | Monitors filesystem for new files
content            | Provides content metadata
discovery          | Discovers new/trending media
scheduler          | Schedules recurring tasks
workflow           | Orchestrates multi-step processes
```

### 7.2 Standard Capabilities

In addition to core-defined capabilities, official modules use these
domain-specific capabilities:

```text
Capability                      | Module Type      | Description
--------------------------------|------------------|----------------------------
metadata.themoviedb             | metadata         | TMDB metadata provider
metadata.imdb                   | metadata         | IMDB metadata provider
metadata.tvdb                   | metadata         | TVDB metadata provider
metadata.tvmaze                 | metadata         | TVMaze metadata provider
metadata.musicbrainz            | metadata         | MusicBrainz provider
metadata.audiodb                | metadata         | TheAudioDB provider
metadata.anidb                  | metadata         | AniDB metadata provider
metadata.mal                    | metadata         | MyAnimeList provider
metadata.openlibrary            | metadata         | Open Library provider
metadata.googlebooks            | metadata         | Google Books provider
downloader.torrent              | downloader       | BitTorrent-based downloader
downloader.usenet               | downloader       | Usenet/NZB-based downloader
downloader.http                 | downloader       | Direct HTTP downloader
downloader.debrid               | downloader       | Debrid service downloader
indexer.torznab                 | indexer          | Torznab-compatible indexer
indexer.newznab                 | indexer          | Newznab-compatible indexer
indexer.general                 | indexer          | General-purpose indexer
transcoder.video                | transcoder       | Video transcoding
transcoder.audio                | transcoder       | Audio transcoding
transcoder.extract              | transcoder       | Subtitle extraction
artwork.poster                  | artwork          | Poster artwork provider
artwork.backdrop                | artwork          | Backdrop artwork provider
artwork.logo                    | artwork          | Logo artwork provider
artwork.clearart                | artwork          | Clear art artwork provider
artwork.thumb                   | artwork          | Thumbnail artwork provider
notification.discord            | notification     | Discord notifications
notification.slack              | notification     | Slack notifications
notification.email              | notification     | Email notifications
notification.webhook            | notification     | Generic webhook notifications
importlist.radarr               | importlist       | Radarr import list
importlist.sonarr               | importlist       | Sonarr import list
importlist.trakt                | importlist       | Trakt import list
importlist.plex                 | importlist       | Plex watchlist import
importlist.jellyfin             | importlist       | Jellyfin watchlist import
resolver.general                | resolver         | Cross-provider ID resolver
quality.standard                | quality          | Standard quality profile
tag.general                     | tag              | Tag-based labeling
```

---

## 8. Error Conventions

### 8.1 Error Codes

When returning errors (via gRPC mesh or event payloads), use `snake_case`
error codes:

```text
Code                         | HTTP analog | Meaning
-----------------------------|-------------|---------------------------------
not_found                    | 404         | Requested item not found
already_exists               | 409         | Item already in library
validation_failed            | 400         | Invalid input parameters
authentication_failed        | 401         | Invalid credentials
authorization_failed         | 403         | Insufficient permissions
rate_limit_exceeded          | 429         | API rate limit hit
quota_exceeded               | 402         | Usage quota exceeded
service_unavailable          | 503         | Remote service down
timeout                      | 504         | Request timed out
internal_error               | 500         | Unspecified internal error
not_implemented              | 501         | Feature not implemented
unsupported_media_type       | 415         | Media format not supported
conflict                     | 409         | State conflict (e.g., already downloading)
cancelled                    | 499         | Operation cancelled
invalid_configuration        | 500         | Module misconfigured
dependency_failed            | 502         | Dependent module error
```

### 8.2 Error Payload Format

When including error details in events or gRPC responses:

```json
{
  "code": "rate_limit_exceeded",
  "message": "TMDB API rate limit reached. Retry in 60 seconds.",
  "details": {
    "retry_after_seconds": "60",
    "provider": "themoviedb",
    "limit": "40",
    "reset_at": "2026-06-10T12:01:00Z"
  }
}
```

---

## 9. Compliance Tiers

Module developers should choose their compliance tier when building:

### Tier 1 — Full Interoperability

- Module ID follows `role-impl` convention
- Roles use one or more from the standard role list
- Capabilities use standard strings where applicable
- Metadata keys follow the standard schema
- Event types follow the `domain.action` convention
- Uses official contract repos where they exist
- `muxcore.json` declares contracts and capabilities

**Result:** Works seamlessly with all official modules. Eligible for the
official marketplace.

### Tier 2 — Domain Compatibility

- Media metadata uses standard field names for shared keys
- Event types are in the right domain namespace
- Settings use standard naming for common config
- You define your own roles, capabilities, and contracts

**Result:** Your modules can interoperate with official modules on the
shared metadata and event boundaries, but are free to extend in private
ways.

### Tier 3 — Private / Custom Module Networks

- You define your own metadata schema
- You use your own event types, role names, and capability strings
- You never need to interoperate with official modules
- Core functions as a generic fabric for your custom module mesh

**Result:** Full flexibility. Your modules are fully functional on core,
just not compatible with the official ecosystem.

---

## 10. Schema Evolution

### 10.1 Adding Fields

New fields may be added to the schema at any time. All new fields are
considered optional. No module should require a field that didn't exist
before.

### 10.2 Deprecating Fields

Fields are deprecated, never removed. Use the `deprecated_` prefix on old
keys while the replacement is introduced:

```text
Old key: imdb_url
New key: imdb_id
Migration: writer emits both keys for one release cycle
```

### 10.3 Versioning Schema Changes

Major breaking changes are communicated through a version key in the metadata:

```text
schema_version: "1.0"  # Current version
```

When a breaking change is necessary (rare), increment the major version.
Modules should check `schema_version` and adapt if needed.

---

## 11. Media Type Dispatch

The `content_type` field is the primary mechanism for dispatching metadata to
the correct handler. Standard values:

```text
content_type    | Schema Section       | Example Modules
----------------|---------------------|-------------------------------
movie           | Section 3.2          | metadata-tmdb, downloader-*
tv              | Section 3.3          | metadata-tvdb, downloader-*
episode         | Section 3.4          | metadata-tvdb, downloader-*
music           | Section 3.5          | metadata-musicbrainz, lidarr
book            | Section 3.6          | metadata-openlibrary, readarr
game            | Section 3.10         | metadata-igdb, downloader-*
podcast         | Section 3.11         | metadata-podcastindex
person          | Section 3.7          | metadata-person
collection      | Common (3.1)         | metadata-tmdb (collections)
other           | Schema-free          | Custom modules
```

When a module processes a metadata map, it should check `content_type` first to
determine which set of fields to expect.

---

## Appendix A: Quick Reference — Common Patterns

```text
What                     | Convention          | Example
-------------------------|---------------------|-----------------------------
External ID field        | snake_case + _id   | imdb_id, tmdb_id, tvdb_id
Module ID                | role-impl          | metadata-tmdb
Role                     | snake_case         | media_manager, downloader
Capability               | domain.sub         | executor.download
Event type               | domain.action      | media.movie.imported
Reply event type         | suffix .reply      | media.movie.lookup.reply
Config key               | snake_case         | download_path, api_key
Error code               | snake_case         | rate_limit_exceeded
Storage meta key         | snake_case         | content_type, media_id
Workflow handler name    | role-impl          | metadata-tmdb
Method (gRPC mesh)       | PascalCase verb    | LookupMovie, StartDownload
Schema namespace marker  | module_name_*      | mymodule_custom_setting
Content type dispatch    | single lowercase   | movie, tv, music, book
