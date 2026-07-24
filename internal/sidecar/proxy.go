package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Route struct {
	PathPrefix string    `json:"path_prefix"`
	TunnelAddr string    `json:"tunnel_addr"`
	ClaimID    string    `json:"claim_id"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type routeTable struct {
	routes    []Route
	etag      string
	updatedAt time.Time
}

type Config struct {
	Listen       string
	AppURL       string
	RoutesURL    string
	SidecarToken string
	PollInterval time.Duration
	MaxStale     time.Duration
	DialTimeout  time.Duration
	MaxBodyBytes int64
}

type Proxy struct {
	app       *url.URL
	cfg       Config
	client    *http.Client
	table     atomic.Pointer[routeTable]
	updateMu  sync.Mutex
	logger    *slog.Logger
	requests  *prometheus.CounterVec
	fetchErrs prometheus.Counter
	routesAge prometheus.Gauge
	registry  *prometheus.Registry
}

func New(cfg Config, logger *slog.Logger) (*Proxy, error) {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 3 * time.Second
	}
	if cfg.MaxStale == 0 {
		cfg.MaxStale = 60 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = time.Second
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = 16 << 20
	}
	app, err := url.Parse(cfg.AppURL)
	if err != nil || app.Scheme == "" || app.Host == "" {
		return nil, fmt.Errorf("invalid app URL %q", cfg.AppURL)
	}
	if logger == nil {
		logger = slog.Default()
	}
	reg := prometheus.NewRegistry()
	p := &Proxy{
		app: app, cfg: cfg, logger: logger, registry: reg,
		client:    &http.Client{Timeout: 5 * time.Second},
		requests:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "devproxy_sidecar_requests_total", Help: "Requests routed by tunlease sidecar."}, []string{"route"}),
		fetchErrs: prometheus.NewCounter(prometheus.CounterOpts{Name: "devproxy_sidecar_route_fetch_errors_total", Help: "Route table fetch failures."}),
		routesAge: prometheus.NewGauge(prometheus.GaugeOpts{Name: "devproxy_sidecar_routes_age_seconds", Help: "Age of the current route table."}),
	}
	reg.MustRegister(p.requests, p.fetchErrs, p.routesAge)
	p.table.Store(&routeTable{})
	return p, nil
}

func (p *Proxy) Registry() *prometheus.Registry { return p.registry }

func (p *Proxy) RunPoller(ctx context.Context) {
	p.fetch(ctx)
	t := time.NewTicker(p.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.fetch(ctx)
		}
	}
}

func (p *Proxy) fetch(ctx context.Context) {
	p.updateMu.Lock()
	defer p.updateMu.Unlock()
	old := p.table.Load()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.RoutesURL, nil)
	if err == nil {
		if old.etag != "" {
			req.Header.Set("If-None-Match", old.etag)
		}
		if p.cfg.SidecarToken != "" {
			req.Header.Set("Authorization", "Bearer "+p.cfg.SidecarToken)
		}
		var resp *http.Response
		resp, err = p.client.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			switch resp.StatusCode {
			case http.StatusNotModified:
				p.table.Store(&routeTable{routes: old.routes, etag: old.etag, updatedAt: time.Now()})
				p.routesAge.Set(0)
				return
			case http.StatusOK:
				var body struct {
					Routes []Route `json:"routes"`
				}
				err = json.NewDecoder(resp.Body).Decode(&body)
				if err == nil {
					sort.Slice(body.Routes, func(i, j int) bool { return len(body.Routes[i].PathPrefix) > len(body.Routes[j].PathPrefix) })
					p.table.Store(&routeTable{routes: body.Routes, etag: resp.Header.Get("ETag"), updatedAt: time.Now()})
					p.routesAge.Set(0)
					return
				}
			default:
			}
		}
	}
	p.fetchErrs.Inc()
	age := time.Since(old.updatedAt)
	if old.updatedAt.IsZero() {
		age = p.cfg.MaxStale + time.Second
	}
	p.routesAge.Set(age.Seconds())
	if age > p.cfg.MaxStale && len(old.routes) > 0 {
		p.logger.Warn("route table stale; clearing for fail-open", "age", age)
		p.table.Store(&routeTable{})
	}
}

func stripWildcard(s string) string { return strings.TrimSuffix(s, "/*") }

func (p *Proxy) Match(path string) (Route, bool) {
	for _, r := range p.table.Load().routes {
		base := stripWildcard(r.PathPrefix)
		if path == base || strings.HasPrefix(path, base+"/") {
			return r, true
		}
	}
	return Route{}, false
}

func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := p.Match(r.URL.Path)
		if !ok {
			p.requests.WithLabelValues("app").Inc()
			p.proxyTo(w, r, p.app, "", nil)
			return
		}
		body, err := readBody(r, p.cfg.MaxBodyBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		target := &url.URL{Scheme: "http", Host: route.TunnelAddr}
		p.proxyTunnel(w, r, target, route.ClaimID, body)
	})
}

func readBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("request body exceeds sidecar replay limit")
	}
	return b, nil
}

func (p *Proxy) proxyTunnel(w http.ResponseWriter, r *http.Request, target *url.URL, claim string, body []byte) {
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	rp := p.reverseProxy(target, claim)
	rp.ModifyResponse = func(*http.Response) error { p.requests.WithLabelValues("tunnel").Inc(); return nil }
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		p.logger.Warn("tunnel failed; falling back to app", "claim_id", claim, "error", err)
		p.requests.WithLabelValues("fallback").Inc()
		p.proxyTo(w, r, p.app, "", body)
	}
	rp.ServeHTTP(w, clone)
}

func (p *Proxy) proxyTo(w http.ResponseWriter, r *http.Request, target *url.URL, claim string, body []byte, handlers ...func(http.ResponseWriter, *http.Request, error)) {
	clone := r.Clone(r.Context())
	if body != nil {
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
	}
	rp := p.reverseProxy(target, claim)
	if len(handlers) > 0 {
		rp.ErrorHandler = handlers[0]
	} else {
		rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		}
	}
	rp.ServeHTTP(w, clone)
}

func (p *Proxy) reverseProxy(target *url.URL, claim string) *httputil.ReverseProxy {
	rp := &httputil.ReverseProxy{
		Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: p.cfg.DialTimeout}).DialContext, ResponseHeaderTimeout: p.cfg.DialTimeout},
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.SetXForwarded()
			if claim != "" {
				req.Out.Header.Set("X-DevProxy-Claim", claim)
			}
		},
	}
	return rp
}
