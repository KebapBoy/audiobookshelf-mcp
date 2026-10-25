# Audiobookshelf MCP Server

A Model Context Protocol (MCP) server that provides tools to interact with your [Audiobookshelf](https://www.audiobookshelf.org/) instance. Access your libraries, audiobooks, podcasts, authors, collections, and playlists through AI assistants that support MCP.

## Disclaimer

**USE AT YOUR OWN RISK:** This software is provided "as is" without warranty of any kind. The author is not responsible for any misuse, accidents, data loss, or any other issues that may arise from using this software. Users are solely responsible for ensuring proper configuration and usage of this tool with their Audiobookshelf instance.

---

## Features

- List and retrieve libraries with optional sub-resources (items, authors, series, search, stats, …)
- Get individual items (audiobooks or podcasts), including cover images as MCP image content
- Browse authors (with author images), series, tags, and genres
- Create and manage collections and playlists
- Browse podcasts, fetch RSS feed metadata, parse OPML, and check for new episodes
- Read and update listening progress
- Inspect users, playback sessions, server status, backups, and filesystem paths
- Run over stdio or the MCP streamable HTTP transport (Docker image included)

## Installation

### Prerequisites

- An Audiobookshelf instance with API access
- An API token from your Audiobookshelf instance

### Build from Source

Build from source or use Docker Compose below to install the server today.

**Prerequisites:**
- Go 1.25.2 or later

```bash
git clone https://github.com/schmidt-software/audiobookshelf-mcp.git
cd audiobookshelf-mcp
go build -o abs-mcp .
```

### Docker Compose

The server can also run as a container that exposes the MCP streamable HTTP transport at `http://localhost:8080/mcp`. See [Running with Docker Compose](#running-with-docker-compose) for setup and configuration.

### Pre-built Releases (once published)

This repository does not currently publish release downloads. Once releases are available, download the appropriate archive for your platform from the [latest release](https://github.com/schmidt-software/audiobookshelf-mcp/releases/latest). Archive names may vary by release.

**Installation steps for future release archives:**

1. Download the appropriate archive for your platform from the releases page
2. Extract the archive, replacing the filename with the archive you downloaded:
   ```bash
   tar -xzf <archive-name>.tar.gz
   ```
3. Move the binary to a location in your PATH (optional but recommended):
   ```bash
   # macOS/Linux
   sudo mv abs-mcp /usr/local/bin/

   # Or to a user directory
   mv abs-mcp ~/.local/bin/
   ```
4. Make it executable (macOS/Linux):
   ```bash
   chmod +x /usr/local/bin/abs-mcp
   ```

## Configuration

The MCP server requires two pieces of configuration:

1. **ABS_BASE_URL** - The server URL of your Audiobookshelf instance, without `/api` (e.g., `https://abs.example.com`, or `https://abs.example.com/abs` when served under a reverse-proxy base path)
2. **ABS_API_KEY** - Your Audiobookshelf API token

### Getting Your API Token

**Audiobookshelf v2.26.0 and later (recommended):**

1. Log into your Audiobookshelf instance as an admin
2. Go to Settings → API Keys
3. Create a new API key, assign it to a user, and copy the key (it is only shown once)

**Older Audiobookshelf versions:**

1. Log into your Audiobookshelf instance
2. Go to Settings → Users → Your User
3. Copy the API token shown for the user

The MCP server can only do what the user behind the key is allowed to do. Some tools (for example `users`, `backups`, `create_backup`, `create_library`, and `filesystem`) require an admin user.

## Usage

### Environment Variables

You can set the configuration using environment variables:

```bash
export ABS_BASE_URL="https://abs.example.com"  # no /api suffix
export ABS_API_KEY="your-api-token-here"
```

Alternatively, you can pass these as parameters when calling tools (see Tool Parameters below).

Transport settings (optional): `MCP_TRANSPORT` (`stdio` by default, `http` for the streamable HTTP transport), `MCP_ADDR` (listen address for `http`, default `:8080`), and `MCP_ENDPOINT` (endpoint path for `http`, default `/mcp`).

### Setting Up with Witsy

[Witsy](https://github.com/nbonamy/witsy) is a desktop AI assistant that supports MCP servers. Here's how to set it up:

1. Open Witsy and go to Settings (⚙️ icon)
2. Navigate to the MCP section
3. Add a new server with the following configuration:

![Witsy MCP Configuration](_res/witsy.png)

**Configuration details:**
- **Type**: `stdio`
- **Label**: `abs-mcp` (or any name you prefer)
- **Command**: `/path/to/abs-mcp` (use the "Pick" button to select your compiled binary)
- **Arguments**: (leave empty)
- **Working Directory**: Any directory (e.g., `/Users/yourname/Downloads`)
- **Environment Variables**:
  - `ABS_BASE_URL` = `https://example.library.abs` (your Audiobookshelf URL, without `/api`)
  - `ABS_API_KEY` = `IM_A_LONG_STRING` (your API token)

4. Click "Save" to add the server
5. The server will now be available in your Witsy conversations!

### Setting Up with Claude Desktop

Add this to your Claude Desktop configuration file:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "audiobookshelf": {
      "command": "/path/to/abs-mcp",
      "env": {
        "ABS_BASE_URL": "https://abs.example.com",
        "ABS_API_KEY": "your-api-token-here"
      }
    }
  }
}
```

## Running with Docker Compose

The repository ships a `Dockerfile` and a `docker-compose.yml` that run the server with the MCP **streamable HTTP** transport instead of stdio. Use this to run the server permanently, for example next to your Audiobookshelf instance, and connect MCP clients over HTTP.

### Prerequisites

- Docker with the Compose plugin (`docker compose`)
- An Audiobookshelf URL that is reachable **from inside the container** (see [Reaching Audiobookshelf from the container](#reaching-audiobookshelf-from-the-container))
- An Audiobookshelf API key (see [Getting Your API Token](#getting-your-api-token))

### Quick Start

```bash
git clone https://github.com/schmidt-software/audiobookshelf-mcp.git
cd audiobookshelf-mcp
cp .env.example .env
# edit .env and set ABS_BASE_URL and ABS_API_KEY
docker compose up -d --build
```

Check that the server is running:

```bash
docker compose ps
docker compose logs abs-mcp   # should print: Listening on :8080/mcp
```

The MCP endpoint is now available at `http://localhost:8080/mcp`.

### Environment Variables

Compose reads these variables from the `.env` file next to `docker-compose.yml` (the file is ignored by git):

| Variable | Required | Default | Description |
|---|---|---|---|
| `ABS_BASE_URL` | yes | – | Audiobookshelf server URL without `/api`, as seen from the container |
| `ABS_API_KEY` | yes | – | Audiobookshelf API key |
| `MCP_PORT` | no | `8080` | Host port that publishes the MCP endpoint |

If a required variable is missing, `docker compose` refuses to start, for example: `required variable ABS_BASE_URL is missing a value: set ABS_BASE_URL`.

The image sets `MCP_TRANSPORT=http` and `MCP_ADDR=:8080`. The endpoint path can be changed with `MCP_ENDPOINT` (default `/mcp`); see [Customizing the setup](#customizing-the-setup).

### Reaching Audiobookshelf from the Container

`localhost` inside the container refers to the container itself, not to your host. Choose `ABS_BASE_URL` depending on where Audiobookshelf runs:

- **Remote server or reverse proxy:** use its public URL, e.g. `https://abs.example.com`.
- **Directly on the Docker host:** with Docker Desktop (macOS/Windows) use `http://host.docker.internal:<port>`. On Linux, additionally map that name to the host in a `docker-compose.override.yml`:

  ```yaml
  services:
    abs-mcp:
      extra_hosts:
        - "host.docker.internal:host-gateway"
  ```

- **Audiobookshelf in another Docker container:** attach `abs-mcp` to the Docker network of the Audiobookshelf container and use its container name, e.g. `ABS_BASE_URL=http://audiobookshelf:80` (use the port Audiobookshelf listens on *inside* its container). Find the network name with `docker network ls`, then add a `docker-compose.override.yml`:

  ```yaml
  services:
    abs-mcp:
      networks:
        - audiobookshelf
  networks:
    audiobookshelf:
      external: true
      name: audiobookshelf_default   # name of the existing network
  ```

### Customizing the Setup

Docker Compose automatically merges a `docker-compose.override.yml` placed next to `docker-compose.yml`, so you can adapt the setup without editing the tracked file. For example, to publish the endpoint on localhost only and serve it under a different path:

```yaml
services:
  abs-mcp:
    environment:
      MCP_ENDPOINT: /audiobookshelf/mcp
    ports: !override
      - "127.0.0.1:${MCP_PORT:-8080}:8080"
```

The `!override` tag replaces the published ports instead of adding to them and requires Docker Compose v2.24.4 or later.

After changing the configuration, apply it with `docker compose up -d`.

> **Security:** The HTTP endpoint has no authentication of its own. Anyone who can reach it can call every tool with the permissions of the configured `ABS_API_KEY`. The default `docker-compose.yml` publishes the port on all host interfaces. Do not expose it to untrusted networks; bind it to localhost as shown above or put it behind an authenticating reverse proxy.

### Connecting MCP Clients

Point clients that support the streamable HTTP transport at the endpoint URL:

```json
{
  "mcpServers": {
    "audiobookshelf": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

The exact configuration format depends on the client. Clients that only support stdio need a stdio-to-HTTP bridge, or can use the native binary as described in [Usage](#usage) instead.

To check the endpoint manually, send an MCP `initialize` request; the server answers with HTTP 200:

```bash
curl -i http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"1.0"}}}'
```

### Updating and Stopping

```bash
git pull
docker compose up -d --build   # rebuild and restart with the latest code

docker compose down            # stop and remove the container
```

## Available Tools

### Libraries

- **libraries** - List all libraries
- **library** - Get a single library by ID, or fetch specific library sub-resources:
  - `items=true` - Get all items in the library
  - `authors=true` - Get all authors in the library
  - `series=true` - Get all series in the library
  - `collections=true` - Get all collections in the library
  - `playlists=true` - Get all playlists in the library
  - `personalized=true` - Get personalized view for the library
  - `filterdata=true` - Get filter data for the library
  - `stats=true` - Get library statistics
  - `search=true` + `query=<text>` - Search library items
  - Optional with search: `limit=<number>` - Limit the number of search results
  - `episode-downloads=true` - Get episode downloads for the library
  - `recent-episodes=true` - Get recent episodes for the library
- **create_library** - Create a new library
  - Required: `name`, `folders` (comma-separated paths), `media_type` (book or podcast)
  - Optional: `icon`, `provider`

### Items

- **item** - Get a single item (audiobook or podcast) by ID, or fetch specific item sub-resources:
  - `cover=true` - Get the cover image for the item as MCP image content
  - `tone-object=true` - Get the tone object for the item

### Authors

- **author** - Get a single author by ID
- **author_image** - Get an author's image by ID as MCP image content

### Series, Tags, and Genres

- **series** - Get a single series by ID
  - Required: `series_id`
- **tags** - List all tags
- **genres** - List all genres

### Collections

- **collections** - List all collections
- **collection** - Get a single collection by ID
- **create_collection** - Create a new collection
  - Required: `library_id`, `name`
  - Optional: `description`
- **add_to_collection** - Add a book to an existing collection
  - Required: `collection_id`, `book_id`

### Playlists

- **playlists** - List all playlists
- **playlist** - Get a single playlist by ID
- **create_playlist** - Create a new playlist
  - Required: `library_id`, `name`
  - Optional: `description`
- **add_to_playlist** - Add an item to an existing playlist
  - Required: `playlist_id`, `item_id`
  - Optional: `episode_id` (for podcast episodes)

### User

- **authorize** - Get authorized user and server information
- **me** - Get authenticated user information, or fetch specific user sub-resources:
  - `listening-sessions=true` - Get listening sessions for the user
  - `listening-stats=true` - Get listening statistics for the user
  - `items-in-progress=true` - Get items currently in progress for the user
  - `progress_item_id=<id>` - Get progress for a specific library item
  - `progress_item_id=<id>` + `progress_episode_id=<id>` - Get progress for a specific episode

### Users

- **users** - List all users
- **users_online** - List currently online users
- **user** - Get a single user by ID, or fetch specific user sub-resources:
  - Required: `user_id`
  - `listening-sessions=true` - Get listening sessions for the user
  - `listening-stats=true` - Get listening statistics for the user

### Sessions

- **sessions** - List all playback sessions
- **session** - Get a single playback session by ID

### Podcasts

- **podcasts** - List podcast library items, fetch feed metadata, or parse OPML text:
  - `library_id=<id>` - Required for listing podcasts; lists items from a podcast library
  - `feed=true`, `rss_feed=<url>` - Fetch podcast RSS feed metadata
  - `opml=true`, `opml_text=<xml>` - Parse OPML text for feed URLs
- **podcast** - Get a podcast library item by ID, or fetch podcast sub-resources:
  - Required: `podcast_id` (the podcast library item ID)
  - `downloads=true` - Get downloads for the podcast
  - `search-episode=true` + `title=<text>` - Search for episodes in the podcast
  - `episode_id=<id>` - Get a specific episode by ID
- **check_podcast_episodes** - Check for new episodes for a podcast
  - Required: `podcast_id` (the podcast library item ID)
  - Optional: `limit` (maximum number of new episodes to download; Audiobookshelf defaults to 3)

### Progress Tracking

- **update_progress** - Update listening progress for a media item
  - Required: `item_id`, `progress` (in seconds)
  - Optional: `duration` (in seconds), `is_finished` (boolean; sends true or false when provided), `episode_id` (for podcasts)

### Item Metadata

- **update_item_media** - Update the metadata of a library item. Only supplied fields are changed; an empty string or array clears a field
  - Required: `item_id`
  - Optional: `title`, `subtitle`, `description`, `publisher`, `language`, `isbn`, `asin`, `published_year`, `published_date`, `explicit` (boolean), `abridged` (boolean), `authors`, `narrators`, `genres`, `tags` (arrays of strings), `series` (array of objects with `name` and optional `sequence`)
  - At least one metadata field is required
- **batch_update_items** - Update the metadata of several library items in one request
  - Required: `updates` (array of objects, each with `item_id` plus the same fields as `update_item_media`)
- **match_item** - Match a library item against a metadata provider and apply the result
  - Required: `item_id`
  - Optional: `provider` (for example `audible.de`; Audiobookshelf falls back to Google Books when omitted), `title`, `author`, `isbn`, `asin`, `override_defaults` (boolean; overwrite fields that already have a value)
- **delete_item** - Delete a library item
  - Required: `item_id`, `confirm` (must be `true`)
  - Optional: `hard` (boolean; also deletes the item's files from disk and cannot be undone). Without it only the database entry is removed, and a rescan can add the item again while its files remain

These tools modify data, so the API key needs a user with update (and for `delete_item` delete) permission.

### Backups

- **backups** - List all server backups
- **create_backup** - Create a server backup

### Server

- **ping** - Simple health check (`GET /ping`)
- **healthcheck** - Server health verification (`GET /healthcheck`)
- **status** - Server initialization status and configuration (`GET /status`)
- **filesystem** - List filesystem paths available to the server

## Tool Parameters

All tools accept optional `base_url` and `token` parameters that override the environment variables. Use the Audiobookshelf server URL without `/api`; a reverse-proxy base path such as `/abs` is supported:

```json
{
  "base_url": "https://abs.example.com",
  "token": "your-api-token-here"
}
```

This is useful if you need to access multiple Audiobookshelf instances or prefer not to use environment variables.

## Example Queries

Once configured, you can ask your AI assistant questions like:

**Reading and browsing:**
- "Show me all my audiobook libraries"
- "What items are in my Fiction library?"
- "Get details about the author with ID abc123"
- "List all my playlists"
- "What's in my Currently Reading collection?"

**Creating and managing:**
- "Create a new collection called 'Summer Reads' in my library"
- "Add this book to my 'Currently Reading' collection"
- "Create a new playlist for my favorite sci-fi audiobooks"
- "Check for new podcast episodes"
- "Update my progress to 45 minutes on this audiobook"
- "Create a backup of my server"

The AI assistant will use the appropriate MCP tools to fetch information and manage your Audiobookshelf instance.

## Development

### Project Structure

- `main.go` - Server implementation: Audiobookshelf HTTP helpers (`absGET`, `absPOST`, `absPATCH`, …), handler factories, tool definitions in `newMCPServer()`, and transport selection in `main()`
- `*_test.go` - Tests against mock Audiobookshelf servers (`httptest`), including tests that call tools through the registered MCP server
- `Dockerfile`, `docker-compose.yml`, `.env.example` - Container image and Compose setup for the HTTP transport
- `.github/workflows/main.yaml` - CI: runs tests and builds on pushes to `main` and on pull requests; publishes releases via GoReleaser on `v*` tags

### Running Tests

```bash
go vet ./...
go test ./...
```

### Adding New Tools

To add a new tool:

1. Define the tool options in `newMCPServer()` using `mcp.NewTool()`
2. Add authentication parameters with `withABSAuth()`
3. Register the tool with `s.AddTool()`
4. Use helper functions like `createSimpleGETHandler()`, `createGETByIDHandler()`, `createGETByIDWithSubResourceHandler()`, or `createSimplePOSTHandler()`
5. Add tests that call the registered tool (see `callRegisteredTool()` and `newRecordingServer()` in `main_test.go`) and document the tool in this README

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE).

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## Support

For issues related to:
- This MCP server: Open an issue in this repository
- Audiobookshelf: Visit [audiobookshelf.org](https://www.audiobookshelf.org/)
- MCP Protocol: See [modelcontextprotocol.io](https://modelcontextprotocol.io/)
