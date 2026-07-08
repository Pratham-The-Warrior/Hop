package main

import (
	"net/http"
	"regexp"
	"strings"
)

// tokenPattern matches the hop transfer token format: word-word-NN
// Two lowercase words separated by hyphens, ending in 2 digits.
var tokenPattern = regexp.MustCompile(`^[a-z]+-[a-z]+-\d{2}$`)

// knownRoutes are the fixed API routes that should NOT be treated as tokens.
var knownRoutes = map[string]bool{
	"/auth":   true,
	"/ws":     true,
	"/signal": true,
	"/tunnel": true,
	"/health": true,
}

// TokenRouter wraps the standard mux and adds token-based and tunnel routing.
// If a request path doesn't match any known route, it checks whether
// the path looks like a transfer token (routes to BrowserBridge) or
// a tunnel slug (routes to TunnelServer).
type TokenRouter struct {
	mux     *http.ServeMux
	browser *BrowserBridge
	tunnel  *TunnelServer
}

// NewTokenRouter creates a TokenRouter that delegates to the given mux
// for known routes, to the BrowserBridge for token-like paths, and to
// the TunnelServer for tunnel paths (/t/<slug>).
func NewTokenRouter(mux *http.ServeMux, browser *BrowserBridge, tunnel *TunnelServer) *TokenRouter {
	return &TokenRouter{
		mux:     mux,
		browser: browser,
		tunnel:  tunnel,
	}
}

// ServeHTTP implements http.Handler.
func (tr *TokenRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Check if this is a known API route — delegate to the standard mux
	if knownRoutes[path] {
		tr.mux.ServeHTTP(w, r)
		return
	}

	// Check for tunnel path: /t/<slug> or /t/<slug>/<subpath>
	if strings.HasPrefix(path, "/t/") {
		trimmed := strings.TrimPrefix(path, "/t/")
		// Split into slug and subpath
		slug := trimmed
		subpath := ""
		if slashIdx := strings.Index(trimmed, "/"); slashIdx != -1 {
			slug = trimmed[:slashIdx]
			subpath = trimmed[slashIdx+1:]
		}

		if tokenPattern.MatchString(slug) {
			tr.tunnel.HandlePublicRequest(w, r, slug, subpath)
			return
		}
	}

	// Check for token download path: /<token>/download
	if strings.HasSuffix(path, "/download") {
		tokenPart := strings.TrimPrefix(path, "/")
		tokenPart = strings.TrimSuffix(tokenPart, "/download")

		if tokenPattern.MatchString(tokenPart) && r.Method == http.MethodGet {
			tr.browser.HandleDownload(w, r, tokenPart)
			return
		}
	}

	// Check for token info path: /<token>
	tokenPart := strings.TrimPrefix(path, "/")
	// Strip trailing slash if present
	tokenPart = strings.TrimRight(tokenPart, "/")

	if tokenPattern.MatchString(tokenPart) && r.Method == http.MethodGet {
		tr.browser.HandleInfo(w, r, tokenPart)
		return
	}

	// Not a known route or token — delegate to mux (will 404)
	tr.mux.ServeHTTP(w, r)
}
