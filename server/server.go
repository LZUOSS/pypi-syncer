package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kexi/pypi-mirror/config"
	"github.com/kexi/pypi-mirror/db"
	"github.com/kexi/pypi-mirror/logging"
)

type voteRequest struct {
	FilePath  string
	IPAddress string
	UserAgent string
}

// Server is the HTTP server for pypi-mirror.
type Server struct {
	cfg            *config.Config
	db             *db.DB
	logger         *logging.AccessLogger
	ipMatcher      *IPMatcher
	trustedProxies []*net.IPNet
	voteCh         chan voteRequest
	upstreamClient *http.Client
	tiers          []config.CacheTier
}

// New creates a new Server.
func New(cfg *config.Config, database *db.DB, logger *logging.AccessLogger) (*Server, error) {
	proxies, err := ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("parse trusted proxies: %w", err)
	}

	matcher, err := NewIPMatcher(cfg.IPModes)
	if err != nil {
		return nil, fmt.Errorf("create IP matcher: %w", err)
	}

	transport, err := config.NewTransport(cfg.Upstream.Proxy)
	if err != nil {
		return nil, fmt.Errorf("create upstream transport: %w", err)
	}
	upstreamClient := &http.Client{Transport: transport}

	return &Server{
		cfg:            cfg,
		db:             database,
		logger:         logger,
		ipMatcher:      matcher,
		trustedProxies: proxies,
		voteCh:         make(chan voteRequest, 1000),
		upstreamClient: upstreamClient,
		tiers:          cfg.EffectiveTiers(),
	}, nil
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	dedupWindow := parseDuration(s.cfg.Cache.DedupWindow)

	// Background vote worker.
	go func() {
		for vr := range s.voteCh {
			prefix := computeIPPrefix(vr.IPAddress)
			if prefix == "" {
				continue
			}
			if err := s.db.RecordVote(vr.FilePath, prefix, dedupWindow); err != nil {
				log.Printf("vote record error: %v", err)
			}
		}
	}()

	mux := http.NewServeMux()

	prefix := s.cfg.Prefix

	mux.HandleFunc(prefix+"/simple/", s.ServeSimple)
	mux.HandleFunc(prefix+"/simple/{pkg}/", s.ServeSimple)
	mux.HandleFunc(prefix+"/packages/", s.ServePackages)
	mux.HandleFunc(prefix+"/web/", s.serveWebRedirect)
	// Catch-all for /{pkg}/json. Registered last (least specific) so the
	// fixed routes above are preferred. Using a wildcard pattern here
	// conflicts with /simple/ in Go 1.22+ ServeMux, so we match manually.
	mux.HandleFunc(prefix+"/", s.serveCatchAll)

	handler := s.loggingMiddleware(mux)

	srv := &http.Server{
		Addr:         s.cfg.Listen,
		Handler:      handler,
		ReadTimeout:  parseDuration(s.cfg.Timeouts.Read),
		WriteTimeout: parseDuration(s.cfg.Timeouts.Write),
		IdleTimeout:  parseDuration(s.cfg.Timeouts.Idle),
	}

	errCh := make(chan error, 1)
	go func() {
		if s.cfg.TLS.Cert != "" {
			errCh <- srv.ListenAndServeTLS(s.cfg.TLS.Cert, s.cfg.TLS.Key)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// serveCatchAll handles paths not matched by any specific route.
// Its only job is routing /{prefix}/{pkg}/json to ServeJSON; everything
// else gets a 404. We cannot register the JSON route as a wildcard pattern
// (prefix+"/{pkg}/json") because Go 1.22 ServeMux detects an ambiguity
// with prefix+"/simple/" — both would match prefix+"/simple/json".
func (s *Server) serveCatchAll(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, s.cfg.Prefix+"/")

	// Root info page.
	if rel == "" {
		s.ServeRoot(w, r)
		return
	}

	// /{pkg}/json endpoint.
	if strings.HasSuffix(rel, "/json") {
		pkg := strings.TrimSuffix(rel, "/json")
		if pkg != "" && !strings.Contains(pkg, "/") {
			s.ServeJSON(w, r)
			return
		}
	}
	http.NotFound(w, r)
}

// serveWebRedirect redirects /pypi/web/ to the upstream PyPI web UI.
func (s *Server) serveWebRedirect(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSuffix(s.cfg.Upstream.PypiURL, "/") + "/" + strings.TrimPrefix(r.URL.Path, s.cfg.Prefix+"/web/")
	http.Redirect(w, r, target, http.StatusFound)
}

// responseCapture wraps http.ResponseWriter to capture status and bytes written.
type responseCapture struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (rc *responseCapture) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	n, err := rc.ResponseWriter.Write(b)
	rc.bytes += int64(n)
	return n, err
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.logger == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rc, r)

		clientIP := ExtractClientIP(r, s.trustedProxies)
		clientIPStr := ""
		if clientIP != nil {
			clientIPStr = clientIP.String()
		}

		s.logger.Write(logging.LogEntry{
			Method:    r.Method,
			Path:      r.URL.Path,
			Status:    rc.status,
			BytesSent: rc.bytes,
			Duration:  time.Since(start),
			ClientIP:  clientIPStr,
			UserAgent: r.Header.Get("User-Agent"),
			Referer:   r.Header.Get("Referer"),
		})
	})
}

// computeIPPrefix computes the network prefix for an IP address.
// IPv4: /24 (zero last byte), IPv6: /48 (keep first 6 bytes).
func computeIPPrefix(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}

	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}

	// IPv6: /48 - keep first 6 bytes, zero the rest.
	v6 := ip.To16()
	for i := 6; i < 16; i++ {
		v6[i] = 0
	}
	return v6.String()
}

// parseDuration parses duration strings, adding support for "d" (day) suffix.
func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return 0
		}
		return time.Duration(n) * 24 * time.Hour
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
