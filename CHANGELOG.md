# Changelog

This is a modified version of a modified version. Each section lists what one fork changed on top of the one before it, newest first, as required by GPL-3.0 section 5(a).

1. [michaeldvinci/audiobookshelf-mcp](https://github.com/michaeldvinci/audiobookshelf-mcp): the original, up to `e790d9c` (2025-11-12)
2. [schmidt-software/audiobookshelf-mcp](https://github.com/schmidt-software/audiobookshelf-mcp): modified 2026-09-03 to 2026-10-03
3. [KebapBoy/audiobookshelf-mcp](https://github.com/KebapBoy/audiobookshelf-mcp): this repository, modified 2026-10-25 to 2026-10-26

## [Unreleased] - KebapBoy fork, 2026-10-26

Based on schmidt-software/audiobookshelf-mcp at `fb1a02c`. Not part of a release.

### Added: item metadata
- **`update_item_media`, `batch_update_items`, `match_item` and `delete_item`** edit, match and delete library items. Only the metadata fields you pass are sent, so omitted fields stay as they are, while an empty string or array clears one. `delete_item` runs only with `confirm: true` and removes files from disk only with `hard: true`.
- **`search_books`** returns a metadata provider's matches with every field it offers, without changing the item, so they can be checked and then saved with `update_item_media`.
- **`compare_covers`** compares the current cover, in its original size, with candidate images by resolution and file size. **`set_item_cover`** sets a cover from a URL. Candidate downloads refuse loopback, private and link-local addresses.
- **`update_author`** changes an author's name, description or Audible ASIN, only the fields you pass. **`set_author_image`** sets an author's portrait from an image URL.

### Added: chapters and tracks
- **`search_chapters`, `update_item_chapters`, `apply_audible_chapters` and `reorder_tracks`.** `apply_audible_chapters` works like the web client: it can keep the existing chapter times and only take the titles, or replace the chapters. Chapter starts close to an audio file boundary snap onto it. Both it and `reorder_tracks` preview by default.
- **Audible intro and outro are detected per book**, by comparing both variants against the file boundaries, unless `remove_branding` is set. The result reports the choice as `branding_removed`.
- **Offsets between the recording and the Audible chapter list are followed.** Chapters inside a file follow the shift of the nearest snapped chapter (`shifted_from`); in `full` mode a running median estimates offsets that change in steps, and is used only when at least half of the chapters agree with it (`audible_start` keeps the original time). The default snap tolerance is 15 seconds instead of 10.
- **`mode: "tracks"`** makes one chapter per audio file, named after the Audible chapter at the same position, and **`stretch_to_audio`** scales the Audible times to the length of the recording.

### Fixed
- **`match_item` overwrites again.** It sent `overrideDefaults`, which Audiobookshelf does not know, so nothing was ever overwritten. It now sends `overrideDetails` and `overrideCover`.

### Changed
- **`match_item` and `update_item_media` return a short summary** (metadata with long texts shortened, plus file and chapter counts) instead of the whole item, which exceeded the tool output limit for long books.
- **The Dockerfile copies all Go files**, not only `main.go`.

## schmidt-software fork, 2026-10-03

Based on michaeldvinci/audiobookshelf-mcp at `e790d9c`. That fork carries no notice of its own; this section is reconstructed from its git history.

### Added
- **Optional streamable HTTP transport** via `MCP_TRANSPORT`, documented as unauthenticated.
- **Dockerfile and Docker Compose setup** with environment configuration.
- **Item covers and author images are returned as MCP image content.**

### Fixed
- **`authorize`** uses POST.
- **`update_progress`** uses `PATCH /me/progress/{itemId}` instead of `POST /me/progress`, and keeps an explicitly given `finished` state.
- **Audiobookshelf base URLs are normalized.**
- **Search tools send the query parameters the API requires.**
- **Podcast tools use the supported endpoints** for checking episodes, fetching feeds, parsing OPML and listing podcasts, and reject blank parameters.
- **Raw GET responses keep their metadata.**

### Changed
- **Tests call the registered tools** against a recording server, and CI runs for pull requests against any branch.
- **README rewritten**: every tool, API key permissions, installation, transport settings and the GPL-3.0 license.
