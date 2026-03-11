# pypi-mirror

A single Go binary that serves as a partial PyPI mirror with popularity-based cache management. It replaces a typical nginx + shadowmire + yukina stack with one self-contained process.

## Overview

`pypi-mirror` provides two subcommands:

- **`serve`** — HTTP server that serves index pages, JSON metadata, and cached packages, falling back to an upstream mirror for uncached files.
- **`sync`** — Run-to-completion sync job: updates the package index from PyPI, then runs cache management to download popular packages and evict unpopular ones.

Both subcommands share a SQLite database (`pypi-mirror.db`) inside `repo_path` for vote tracking, package serials, and cached file sizes.

## Quick Start

### Build

```sh
go build -o pypi-mirror .
```

No CGO is required. The SQLite driver (`modernc.org/sqlite`) is a pure-Go implementation.

### Configure

Copy the example config and edit it:

```sh
cp config.example.yaml config.yaml
$EDITOR config.yaml
```

At minimum set `repo_path` and `upstream.packages_url`.

### Run

Start the HTTP server:

```sh
./pypi-mirror serve -c config.yaml
```

Run a sync (index + cache):

```sh
./pypi-mirror sync -c config.yaml
```

Run sync on a schedule (e.g. via systemd timer or cron) to keep the index and cache up to date.

## CLI Usage

### `serve`

```
pypi-mirror serve [flags]

Flags:
  -c, --config string   Path to config file (default "config.yaml")
```

Starts the HTTP server. Listens on the address from `listen`, serves PyPI endpoints under the configured `prefix`. Gracefully shuts down on SIGINT/SIGTERM (30-second grace period). Sends SIGUSR1 to reopen the log file.

### `sync`

```
pypi-mirror sync [flags]

Flags:
  -c, --config string   Path to config file (default "config.yaml")
```

Runs two phases in sequence then exits:

1. **Index sync** — fetches all package serials from PyPI via XML-RPC, updates local index pages and JSON metadata for new/changed packages, removes deleted packages.
2. **Cache management** — runs phases A–E to download popular packages and evict unpopular ones within the configured size limit.

Handles SIGINT/SIGTERM by cancelling the context (sync stops at the next cancellation point).

## Configuration Reference

All configuration is in YAML. Duration values accept Go duration syntax (`30s`, `5m`, `2h`) plus a `d` suffix for days (`7d`, `2d`).

### Top-level keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `listen` | string | `":8080"` | TCP address to listen on (e.g. `":8080"`, `"127.0.0.1:8080"`) |
| `repo_path` | string | _(required)_ | Directory where the mirror data is stored |
| `prefix` | string | `"/pypi"` | URL path prefix for all endpoints |

### `upstream`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `pypi_url` | string | `"https://pypi.org"` | Base URL for PyPI index and JSON API |
| `packages_url` | string | _(required)_ | Base URL for package files — used by the sync downloader and proxy-mode requests |
| `redirect_url` | string | _(same as `packages_url`)_ | Base URL clients are redirected to in `"302"` mode. Set when the redirect target should differ from the internal download source (e.g. a CDN or the canonical `files.pythonhosted.org`) |
| `proxy` | string | — | HTTP/HTTPS/SOCKS5 proxy for all outbound requests (sync downloads, HEAD requests, and server-side upstream proxy). Supports `http://`, `https://`, and `socks5://` URLs. Leave unset for a direct connection. |

### `tls`

Optional. Omit to use plain HTTP.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `cert` | string | — | Path to TLS certificate file |
| `key` | string | — | Path to TLS private key file |

### `trusted_proxies`

List of CIDR ranges (e.g. `"10.0.0.0/8"`) whose `X-Forwarded-For` header is trusted when determining the real client IP.

### `ip_modes`

Controls whether uncached package requests are transparently proxied or redirected per client IP.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `default` | string | `"302"` | Default mode for IPs not matching any rule: `"302"` (redirect) or `"proxy"` |
| `rules` | list | — | Per-CIDR overrides |

Each rule:

| Key | Type | Description |
|-----|------|-------------|
| `cidr` | string | IPv4 or IPv6 CIDR block |
| `mode` | string | `"302"` or `"proxy"` |

In `"302"` mode the client is redirected to `packages_url`. In `"proxy"` mode the server fetches the file from `packages_url` and streams it back to the client.

### `cache`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `size_limit` | size | — | Maximum total size of locally cached packages when `tiers` is not set (e.g. `"512g"`, `"1t"`) |
| `filesize_limit` | size | — | Files larger than this are never downloaded (e.g. `"4g"`) |
| `min_vote_count` | int | `2` | Minimum vote count within the vote window for a file to be considered for download |
| `vote_window` | duration | `"7d"` | Rolling window over which votes are counted |
| `dedup_window` | duration | `"5m"` | Votes from the same IP prefix within this window count only once per file |
| `size_db_ttl` | duration | `"2d"` | TTL for cached remote file size records |
| `tiers` | list | — | Multi-tier cache configuration (see below). When set, `size_limit` is ignored. |

Size values accept suffixes: `k`/`kb`, `m`/`mb`, `g`/`gb`, `t`/`tb` (case-insensitive).

#### `cache.tiers` (optional)

A list of cache tiers ordered from hottest (tier 0, typically SSD) to coldest (last tier, typically HDD). Files are assigned to the hottest tier with remaining capacity; files that do not fit in any tier are deleted.

| Key | Type | Description |
|-----|------|-------------|
| `path` | string | Absolute path to the directory for this tier. Must be created before running. |
| `size_limit` | size | Maximum total size for this tier. |

Example:

```yaml
cache:
  tiers:
    - path: "/mnt/ssd/pypi/packages"
      size_limit: "100g"
    - path: "/mnt/hdd/pypi/packages"
      size_limit: "2t"
```

When tiers are on different filesystems, file promotion/demotion falls back to a copy+delete operation automatically.

### `sync`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `retry` | int | `3` | Number of download retries on failure |
| `download_error_threshold` | int | `5` | Stop cache phase D after this many consecutive download errors |
| `user_agent` | string | `"pypi-mirror/1.0"` | User-Agent header sent to upstream |
| `concurrent_downloads` | int | `4` | Number of concurrent package index fetches during index sync |

### `log`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `path` | string | — | Path to access log file. If empty, access logging is disabled |
| `format` | string | `"mirror-json"` | Log format: `"mirror-json"` or `"combined"` |

### `timeouts`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `read` | duration | `"30s"` | HTTP server read timeout |
| `write` | duration | `"120s"` | HTTP server write timeout |
| `idle` | duration | `"60s"` | HTTP server idle (keep-alive) timeout |
| `upstream` | duration | `"60s"` | Timeout for upstream proxy requests |

## On-Disk Layout

```
{repo_path}/
  simple/
    index.html          # Root simple index (PEP 503 HTML)
    index.v1.json       # Root simple index (PEP 691 JSON)
    {pkg-name}/
      index.html        # Per-package simple page
      index.v1.json     # Per-package simple page (JSON)
  json/
    {pkg-name}          # Raw PyPI JSON metadata (from /pypi/{pkg}/json)
  packages/             # Default single-tier cache (when cache.tiers is not set)
    {ab}/{abcd…}/       # Package files, mirroring PyPI's layout
      {filename}
  pypi-mirror.db        # SQLite database
```

When `cache.tiers` is configured, each tier has its own directory (e.g. `/mnt/ssd/pypi/packages/`). The `{repo_path}/packages/` directory is not used in that case.

## Architecture

### `serve`

The server exposes the endpoints listed below under the configured `prefix`. Every request goes through a logging middleware that captures status, bytes sent, duration, and client IP.

For package file requests the server:
1. Records a vote asynchronously (via a buffered channel with capacity 1000) unless the client is a known bot.
2. Serves the file from `packages/` if it exists locally.
3. Otherwise proxies or redirects to `packages_url` based on the client's IP mode.

The vote channel is drained by a single background goroutine that writes to the SQLite database with deduplication.

### `sync`

The sync command is intended to be run periodically (e.g. every hour via a systemd timer). It:

1. Calls PyPI's XML-RPC `list_packages_with_serial` to get the current serial for every package.
2. Compares with locally stored serials to find new, updated, and removed packages.
3. Fetches JSON metadata and generates simple index pages for new/updated packages (concurrently, up to `concurrent_downloads` goroutines).
4. Runs cache management phases A–E (see below).

## HTTP Endpoints

All routes are registered under `{prefix}` (default `/pypi`).

| Method | Path | Description |
|--------|------|-------------|
| GET | `{prefix}/simple/` | Root simple index (PEP 503/691 content negotiation) |
| GET | `{prefix}/simple/{pkg}/` | Per-package simple index |
| GET | `{prefix}/packages/…` | Package file serving (cached local or upstream fallback) |
| GET | `{prefix}/{pkg}/json` | Package JSON metadata (proxied from upstream or served locally) |
| GET | `{prefix}/web/…` | Redirect to the upstream PyPI web UI |

Content negotiation on `/simple/` endpoints: if the client sends `Accept: application/vnd.pypi.simple.v1+json`, the JSON form (PEP 691) is served; otherwise the HTML form (PEP 503) is served.

## Popularity Tracking

When a client downloads a package file from `{prefix}/packages/…`:

1. The client's real IP is extracted (honoring `X-Forwarded-For` from trusted proxies).
2. The IP is collapsed to a network prefix for deduplication:
   - IPv4: `/24` (last octet zeroed)
   - IPv6: `/48` (last 10 bytes zeroed)
3. If the `User-Agent` contains any of `bandersnatch`, `Googlebot`, `bingbot`, `YandexBot`, or `Baiduspider`, the request is not recorded.
4. Otherwise a vote request is sent non-blocking to an internal channel. A background goroutine writes it to the `votes` table, skipping the write if an identical `(file, ip_prefix)` pair was already recorded within the `dedup_window`.

## Cache Management

Runs in five phases each time `sync` is invoked:

**Phase A — Inventory**
Walk each tier's directory and record the size and tier index of each local file in the `local_sizes` table (cached to avoid repeated `stat` calls). For single-tier configs (no `tiers` key), this is equivalent to walking `{repo_path}/packages/`.

**Phase B — Resolve remote sizes**
Query the `votes` table for files with at least `min_vote_count` unique IP-prefix votes in the last `vote_window`. For popular files not present locally, issue a HEAD request to `packages_url` to determine their size. Results are cached in `remote_sizes` for `size_db_ttl`. Files larger than `filesize_limit` are excluded.

**Phase C — Score**
Score every file (local and popular-but-missing) using:

```
score = voteCount / (max(size, 2 GiB) + 1) * 1048576
```

Higher score = more popular relative to size.

**Phase D — Assign and execute (two passes)**

*Assignment pass:* Sort all files by score descending. Walk the sorted list and assign each file to the hottest (first) tier with remaining capacity. Files that do not fit in any tier are marked for deletion.

*Execution pass:* For each file:
- Remote + assigned → download to the target tier directory.
- Local + correct tier → no-op.
- Local + needs promotion/demotion → move to the assigned tier (`os.Rename`; falls back to copy+delete across filesystems).
- Local + no assignment → delete from disk and DB.

Stop downloading if `download_error_threshold` errors occur.

**Phase E — Cleanup**
Delete votes older than `vote_window`, expire `remote_sizes` records older than `size_db_ttl`, and remove `local_sizes` entries for files no longer on disk.

## Index Syncing

The `sync` subcommand maintains a local copy of every package's simple index and JSON metadata:

1. **Serial tracking** — The `serials` table stores the last-known serial for each package. On each sync run, PyPI's XML-RPC API returns the current serial for all packages. Only packages with a higher remote serial are re-fetched.
2. **Per-package sync** — For each outdated package, the JSON API (`/pypi/{pkg}/json`) is fetched and stored under `json/{normalized-name}`. Simple index pages (PEP 503 HTML and PEP 691 JSON) are generated in `simple/{normalized-name}/`.
3. **Root index** — After processing all packages, the root `simple/index.html` and `simple/index.v1.json` are regenerated listing all known packages.
4. **Removed packages** — Packages present locally but absent from the remote serial list have their `simple/` and `json/` entries removed and their serial deleted from the DB.

Package names are normalized per PEP 503 (lowercased, runs of `[-_.]` replaced with `-`).

## Logging

Access logs are written to `log.path` (if configured). Two formats are supported:

**`mirror-json`** (default) — one JSON object per line:

```json
{"time":"2026-01-01T00:00:00Z","method":"GET","path":"/pypi/packages/…","status":200,"bytes":1234567,"duration_ms":42,"client_ip":"1.2.3.4","user_agent":"pip/…","referer":"","proxied":"0"}
```

**`combined`** — Apache combined log format:

```
1.2.3.4 - - [01/Jan/2026:00:00:00 +0000] "GET /pypi/packages/… HTTP/1.1" 200 1234567 "" "pip/…"
```

The log file is written through a buffered writer that auto-flushes every second. Send **SIGUSR1** to the `serve` process to reopen the log file (for use with logrotate's `copytruncate`-free rotation).

## Deployment

### Building

```sh
go build -o pypi-mirror .
```

No external C libraries are required.

### Directory setup

```sh
install -d -m 0755 -o mirror -g mirror /srv/repo/pypi
install -d -m 0755 /var/log/pypi-mirror
```

### Running directly

```sh
./pypi-mirror serve -c /etc/pypi-mirror/config.yaml
```

Run sync periodically:

```sh
./pypi-mirror sync -c /etc/pypi-mirror/config.yaml
```

### TLS

Set `tls.cert` and `tls.key` in the config to enable TLS directly in pypi-mirror. Alternatively, terminate TLS at a reverse proxy (nginx, Caddy, etc.) and leave pypi-mirror on plain HTTP on a loopback or internal address.
