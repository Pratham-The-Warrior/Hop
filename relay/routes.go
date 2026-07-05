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
	"/health": true,
}

// TokenRouter wraps the standard mux and adds token-based routing.
// If a request path doesn't match any known route, it checks whether
// the path looks like a transfer token and routes to the BrowserBridge.
type TokenRouter struct {
	mux     *http.ServeMux
	browser *BrowserBridge
}

// NewTokenRouter creates a TokenRouter that delegates to the given mux
// for known routes and to the BrowserBridge for token-like paths.
func NewTokenRouter(mux *http.ServeMux, browser *BrowserBridge) *TokenRouter {
	return &TokenRouter{
		mux:     mux,
		browser: browser,
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
