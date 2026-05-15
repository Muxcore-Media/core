# Marketplace

Modules are discovered through marketplace catalogs. A marketplace is a git repo with a `catalog.json` file listing module repo URLs.

## Official Marketplace

The official MuxCore marketplace is at `https://github.com/Muxcore-Media/marketplace-catalog`.

Add this URL in your MuxCore config to discover all official modules.

## How It Works

1. User adds a marketplace URL (a git repo)
2. MuxCore fetches `catalog.json` from the repo
3. For each module URL in the catalog, MuxCore fetches `muxcore.json` from the module repo
4. Module metadata is displayed in the admin UI marketplace browser
5. User selects modules to install

## Official vs Third-Party

**Official modules** are repos owned by the `Muxcore-Media` GitHub organization. The admin UI displays an Official badge for these.

**Third-party modules** are repos from any other org or user. Users add third-party marketplace URLs to discover them.

This distinction is purely based on the GitHub org — no verification key, no signing, no approval process. Module URLs are checked: if they contain `github.com/Muxcore-Media`, the module is official.

## catalog.json Format

```json
{
  "name": "Marketplace Name",
  "description": "Description of this marketplace",
  "modules": [
    "https://github.com/Muxcore-Media/admin-ui",
    "https://github.com/Muxcore-Media/downloader-qbittorrent",
    "https://github.com/thirdparty/custom-module"
  ]
}
```

## muxcore.json Format

Each module repo must have a `muxcore.json` at its root:

```json
{
  "name": "Module Name",
  "description": "What the module does",
  "version": "1.0.0",
  "icon": "https://example.com/icon.png",
  "author": "Author Name",
  "kind": "downloader",
  "capabilities": ["downloader.torrent", "downloader.qbittorrent"],
  "dependencies": ["api-rest"],
  "homepage": "https://github.com/org/repo"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Display name in the marketplace |
| `description` | Yes | What the module does |
| `version` | Yes | Semantic version |
| `icon` | No | URL to an icon image |
| `author` | Yes | Module author name |
| `kind` | Yes | Module kind: `auth`, `provider`, `downloader`, `media_manager`, `processor`, `playback`, `workflow`, `storage`, `ui`, `api`, `eventbus`, `scheduler` |
| `capabilities` | Yes | Array of capability strings |
| `dependencies` | No | Module IDs this module depends on |
| `homepage` | No | URL to the module's repo or website |

## Creating a Third-Party Marketplace

1. Create a git repo
2. Add a `catalog.json` listing your module repos
3. Tell users to add your repo URL to their MuxCore config

That's it. No registration, no approval. Your modules show up alongside official ones, with a clear indication they're third-party.

## Creating a Module for the Marketplace

1. Create a Go module implementing the relevant contract interfaces from `github.com/Muxcore-Media/core/pkg/contracts`
2. Add `muxcore.json` at the repo root
3. Call `contracts.Register()` in your `init()` function
4. Add the repo URL to a marketplace catalog (official or your own)
5. Users discover it, install it, and it registers itself at startup
