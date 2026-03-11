# Deployment Guide

This guide covers production deployment of `pypi-mirror` on a systemd-based Linux system.

## MySQL / MariaDB Setup

pypi-mirror stores all state (votes, package serials, cached file sizes) in
MySQL/MariaDB. Both the `serve` and `sync` processes connect to the same
database concurrently; MySQL handles this natively.

```sql
-- Run as MySQL root
CREATE DATABASE pypi_mirror DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'mirror'@'localhost' IDENTIFIED BY 'changeme';
GRANT ALL PRIVILEGES ON pypi_mirror.* TO 'mirror'@'localhost';
FLUSH PRIVILEGES;
```

Set the DSN in `config.yaml`:

```yaml
database:
  dsn: "mirror:changeme@tcp(127.0.0.1:3306)/pypi_mirror?parseTime=true&charset=utf8mb4"
```

The schema is created automatically on first startup.

## Directory and User Setup

```sh
# Create a dedicated service user
useradd -r -s /sbin/nologin -d /srv/repo/pypi mirror

# Create data and log directories
install -d -m 0755 -o mirror -g mirror /srv/repo/pypi
install -d -m 0750 -o mirror -g mirror /etc/pypi-mirror
install -d -m 0755 -o root  -g adm    /var/log/pypi-mirror

# Install the binary
install -m 0755 pypi-mirror /usr/local/bin/pypi-mirror

# Install config
install -m 0640 -o root -g mirror config.yaml /etc/pypi-mirror/config.yaml
```

### Multi-tier cache directories

If you configure `cache.tiers`, create each tier directory before starting:

```sh
install -d -m 0755 -o mirror -g mirror /mnt/ssd/pypi/packages
install -d -m 0755 -o mirror -g mirror /mnt/hdd/pypi/packages
```

Also add the tier paths to `ReadWritePaths` in the systemd service units (see below).

### Upstream proxy

If pypi-mirror needs to reach upstream PyPI or the packages mirror through an HTTP/HTTPS/SOCKS5 proxy, set `upstream.proxy` in `config.yaml`:

```yaml
upstream:
  proxy: "socks5://127.0.0.1:1080"
```

This applies to all outbound connections: index sync, HEAD size-resolution requests, and server-side reverse-proxy requests (when `ip_modes` is `"proxy"`).

Relevant `config.yaml` settings for this layout:

```yaml
repo_path: "/srv/repo/pypi"
log:
  path: "/var/log/pypi-mirror/access.log"
```

## systemd Units

### `serve` — persistent HTTP server

`/etc/systemd/system/pypi-mirror-serve.service`:

```ini
[Unit]
Description=PyPI mirror HTTP server
After=network.target

[Service]
Type=simple
User=mirror
Group=mirror
ExecStart=/usr/local/bin/pypi-mirror serve -c /etc/pypi-mirror/config.yaml
Restart=on-failure
RestartSec=5s

# Log rotation: send SIGUSR1 to reopen the access log
ExecReload=/bin/kill -USR1 $MAINPID

# Harden the service
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
# Add any cache.tiers paths here if using multi-tier cache
ReadWritePaths=/srv/repo/pypi /var/log/pypi-mirror

[Install]
WantedBy=multi-user.target
```

### `sync` — periodic index and cache sync

Two files are needed: a service unit and a timer unit.

`/etc/systemd/system/pypi-mirror-sync.service`:

```ini
[Unit]
Description=PyPI mirror index and cache sync
After=network.target

[Service]
Type=oneshot
User=mirror
Group=mirror
ExecStart=/usr/local/bin/pypi-mirror sync -c /etc/pypi-mirror/config.yaml

# Harden the service
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
# Add any cache.tiers paths here if using multi-tier cache
ReadWritePaths=/srv/repo/pypi
```

`/etc/systemd/system/pypi-mirror-sync.timer`:

```ini
[Unit]
Description=Run PyPI mirror sync hourly

[Timer]
OnBootSec=5min
OnUnitActiveSec=1h
RandomizedDelaySec=120
Persistent=true

[Install]
WantedBy=timers.target
```

Enable and start:

```sh
systemctl daemon-reload

systemctl enable --now pypi-mirror-serve.service
systemctl enable --now pypi-mirror-sync.timer
```

Check status:

```sh
systemctl status pypi-mirror-serve
systemctl status pypi-mirror-sync.timer
journalctl -u pypi-mirror-sync -f
```

## Log Rotation

Create `/etc/logrotate.d/pypi-mirror`:

```
/var/log/pypi-mirror/access.log {
    daily
    rotate 30
    compress
    delaycompress
    missingok
    notifempty
    sharedscripts
    postrotate
        systemctl kill -s USR1 pypi-mirror-serve.service
    endscript
}
```

The `serve` process handles SIGUSR1 by flushing buffered data and reopening the log file at the same path. No `copytruncate` is needed.

## Nginx Reverse Proxy (optional)

If you want to terminate TLS or serve on port 443 with nginx in front of pypi-mirror:

```nginx
upstream pypi_mirror {
    server 127.0.0.1:8080;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name pypi.example.com;

    ssl_certificate     /etc/ssl/certs/pypi.example.com.pem;
    ssl_certificate_key /etc/ssl/private/pypi.example.com.key;

    # Pass real client IP to pypi-mirror
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header Host $host;

    location /pypi/ {
        proxy_pass         http://pypi_mirror;
        proxy_http_version 1.1;
        proxy_set_header   Connection "";

        # Allow large package downloads
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_buffering    off;
    }
}
```

Set `trusted_proxies` in `config.yaml` to include the nginx host IP (or `127.0.0.1/8`) so that `X-Forwarded-For` is trusted for client IP extraction and vote deduplication.

## TLS Directly in pypi-mirror

To have pypi-mirror terminate TLS itself (without nginx), set in `config.yaml`:

```yaml
listen: ":443"
tls:
  cert: "/etc/ssl/certs/pypi.example.com.pem"
  key:  "/etc/ssl/private/pypi.example.com.key"
```

Grant the `mirror` user read access to the key:

```sh
chgrp mirror /etc/ssl/private/pypi.example.com.key
chmod 0640   /etc/ssl/private/pypi.example.com.key
```

Or use `AmbientCapabilities=CAP_NET_BIND_SERVICE` in the systemd unit instead of running as root.

## Migration from nginx + shadowmire + yukina

1. **Stop the old services** (`nginx`, `shadowmire`, `yukina` timer).

2. **Keep existing data.** The `packages/` tree from shadowmire/bandersnatch can be reused as-is. Point `repo_path` at the directory containing it.

3. **Build and install** pypi-mirror:
   ```sh
   go build -o /usr/local/bin/pypi-mirror .
   ```

4. **Write `config.yaml`** (see `config.example.yaml`). Set:
   - `repo_path` to your existing mirror root.
   - `upstream.packages_url` to the packages mirror URL previously used by your nginx config.
   - `upstream.pypi_url` to `https://pypi.org` (or a trusted mirror).
   - `trusted_proxies` and `ip_modes` to match your existing nginx `proxy_pass` / `return 302` logic.
   - `cache.size_limit` to match your available disk space.

5. **Run the first sync** to populate the index and SQLite DB:
   ```sh
   sudo -u mirror /usr/local/bin/pypi-mirror sync -c /etc/pypi-mirror/config.yaml
   ```
   This may take several minutes for the first run (all serials are new).

6. **Start the serve unit** and verify endpoints respond:
   ```sh
   systemctl start pypi-mirror-serve
   curl http://localhost:8080/pypi/simple/pip/
   ```

7. **Update nginx** (or DNS) to forward `/pypi/` traffic to pypi-mirror and remove the old proxy rules, or remove nginx entirely if pypi-mirror is handling TLS directly.

8. **Enable the sync timer** and remove old cron jobs / timers for shadowmire and yukina:
   ```sh
   systemctl enable --now pypi-mirror-sync.timer
   ```
