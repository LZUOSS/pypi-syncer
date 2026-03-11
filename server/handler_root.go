package server

import (
	"fmt"
	"html"
	"net/http"
	"time"
)

// ServeRoot renders the mirror status/info page at {prefix}/.
func (s *Server) ServeRoot(w http.ResponseWriter, r *http.Request) {
	clientIP := ExtractClientIP(r, s.trustedProxies)
	mode := s.cfg.IPModes.Default
	if clientIP != nil {
		mode = s.ipMatcher.Match(clientIP)
	}

	clientIPStr := "unknown"
	if clientIP != nil {
		clientIPStr = clientIP.String()
	}

	redirectBase := s.cfg.Upstream.RedirectURL
	if redirectBase == "" {
		redirectBase = s.cfg.Upstream.PackagesURL
	}

	var modeBadge, modeDetail string
	if mode == "proxy" {
		modeBadge = `<span class="badge proxy">proxy</span>`
		modeDetail = fmt.Sprintf(
			"Uncached packages are fetched from <code>%s</code> by the server and streamed to you.",
			html.EscapeString(s.cfg.Upstream.PackagesURL),
		)
	} else {
		modeBadge = `<span class="badge redirect">302 redirect</span>`
		modeDetail = fmt.Sprintf(
			"Uncached packages redirect your client to <code>%s</code> to download directly.",
			html.EscapeString(redirectBase),
		)
	}

	prefix := html.EscapeString(s.cfg.Prefix)

	// Determine the public-facing host. Prefer X-Forwarded-Host (set by
	// reverse proxies like nginx) over the backend listen address.
	publicHost := r.Header.Get("X-Forwarded-Host")
	if publicHost == "" {
		publicHost = r.Host
	}
	host := html.EscapeString(publicHost)

	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	baseURL := scheme + "://" + publicHost

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>PyPI Mirror — %s</title>
<style>
  *{box-sizing:border-box}
  body{font-family:system-ui,sans-serif;margin:0;background:#f8f9fa;color:#212529}
  header{background:#0d6efd;color:#fff;padding:1.25rem 2rem}
  header h1{margin:0;font-size:1.4rem;font-weight:600}
  header p{margin:.25rem 0 0;opacity:.85;font-size:.9rem}
  main{max-width:860px;margin:2rem auto;padding:0 1.5rem}
  section{background:#fff;border:1px solid #dee2e6;border-radius:.5rem;padding:1.25rem 1.5rem;margin-bottom:1.5rem}
  section h2{margin:0 0 1rem;font-size:1rem;font-weight:600;text-transform:uppercase;letter-spacing:.05em;color:#6c757d}
  .ok{color:#198754;font-weight:600}
  .badge{display:inline-block;padding:.2em .55em;border-radius:.35em;font-size:.85em;font-weight:600}
  .badge.proxy{background:#fff3cd;color:#664d03}
  .badge.redirect{background:#cfe2ff;color:#084298}
  table{width:100%%;border-collapse:collapse;font-size:.9rem}
  td,th{padding:.45rem .6rem;border-bottom:1px solid #f0f0f0;text-align:left}
  tr:last-child td{border-bottom:none}
  th{color:#6c757d;font-weight:600;width:35%%}
  code,pre{background:#f1f3f5;border-radius:.3em;padding:.15em .4em;font-size:.875em}
  pre{padding:.75rem 1rem;overflow-x:auto;line-height:1.5}
  .mono{font-family:monospace}
</style>
</head>
<body>
<header>
  <h1>PyPI Mirror</h1>
  <p>%s</p>
</header>
<main>

<section>
  <h2>Status</h2>
  <table>
    <tr><th>Mirror status</th><td><span class="ok">&#10003; Running</span></td></tr>
    <tr><th>Server time</th><td class="mono">%s</td></tr>
    <tr><th>Your IP</th><td class="mono">%s</td></tr>
    <tr><th>Your access mode</th><td>%s &nbsp; %s</td></tr>
  </table>
</section>

<section>
  <h2>Usage</h2>
  <p>Configure pip to use this mirror:</p>
  <pre>pip install --index-url %s%s/simple/ &lt;package&gt;</pre>
  <p>Or set it permanently in <code>~/.pip/pip.conf</code> (Linux/macOS) or <code>%%APPDATA%%\pip\pip.ini</code> (Windows):</p>
  <pre>[global]
index-url = %s%s/simple/</pre>
  <p>Or in <code>pyproject.toml</code> / <code>uv</code>:</p>
  <pre>[[tool.uv.index]]
url = "%s%s/simple/"</pre>
</section>

<section>
  <h2>Endpoints</h2>
  <table>
    <tr><th><code>%s/simple/</code></th><td>PEP 503/691 simple index (used by pip)</td></tr>
    <tr><th><code>%s/simple/{pkg}/</code></th><td>Per-package simple index</td></tr>
    <tr><th><code>%s/packages/…</code></th><td>Package files (cached locally or forwarded upstream)</td></tr>
    <tr><th><code>%s/{pkg}/json</code></th><td>PyPI JSON metadata API</td></tr>
    <tr><th><code>%s/web/</code></th><td>Redirect to upstream PyPI web UI</td></tr>
  </table>
</section>

</main>
</body>
</html>
`,
		host,
		host,
		time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		html.EscapeString(clientIPStr),
		modeBadge,
		modeDetail, // already contains trusted HTML (<code>…</code>), do not escape
		baseURL, prefix,
		baseURL, prefix,
		baseURL, prefix,
		prefix, prefix, prefix, prefix, prefix,
	)
}
