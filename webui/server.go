// Package webui implements the embedded, read-only web graph explorer for
// known: a localhost HTTP server exposing a JSON API over the storage
// repositories and query engine, plus the embedded frontend assets.
//
// The API surface, wire shapes, and command flags are specified by the
// binding contract shared with the frontend slice (known-au7); see the
// contract doc referenced from bead known-621.
package webui

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

// Server serves the read-only graph explorer: the JSON API under /api/* and
// the embedded frontend assets everywhere else. Server implements
// http.Handler directly (via ServeHTTP), so it can be used wherever an
// http.Handler is expected without a separate accessor.
type Server struct {
	entries storage.EntryRepo
	edges   storage.EdgeRepo
	scopes  storage.ScopeRepo
	labels  storage.LabelLister
	engine  *query.Engine

	defaultScope string
	assets       fs.FS
	mux          *http.ServeMux

	httpServer *http.Server
}

// New constructs a Server from the storage repositories, query engine, and
// default scope (pre-selected by the UI, exposed via /api/meta). labels may
// be nil when the backend does not implement storage.LabelLister; /api/meta
// then reports an empty label list.
func New(entries storage.EntryRepo, edges storage.EdgeRepo, scopes storage.ScopeRepo, labels storage.LabelLister, engine *query.Engine, defaultScope string) *Server {
	assets, err := fs.Sub(Assets, "assets")
	if err != nil {
		// webui/embed.go's "//go:embed all:assets" guarantees an "assets"
		// directory exists at build time; this can only fail if the embed
		// directive itself is broken.
		panic("webui: embedded assets missing \"assets\" directory: " + err.Error())
	}

	s := &Server{
		entries:      entries,
		edges:        edges,
		scopes:       scopes,
		labels:       labels,
		engine:       engine,
		defaultScope: defaultScope,
		assets:       assets,
	}
	s.mux = s.routes()
	return s
}

// routes builds the request-routing table. Go 1.22+ ServeMux method and
// wildcard patterns are used throughout. "/" is a catch-all that fires for
// every request no more specific pattern claims — including /api/* requests
// with an unregistered method or an unknown sub-path, since ServeMux falls
// through to "/" rather than synthesizing a 405/404 on its own. handleRoot
// intercepts exactly that case so /api/* never leaks a static/HTML response.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/graph", s.handleGraph)
	mux.HandleFunc("GET /api/entry/{id}", s.handleEntry)
	mux.HandleFunc("GET /api/neighbors/{id}", s.handleNeighbors)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/path", s.handlePath)
	mux.HandleFunc("/", s.handleRoot)
	return mux
}

// ServeHTTP implements http.Handler by delegating to the routing table.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// apiPrefix is the path prefix reserved for the JSON API. Every response
// under it MUST be application/json per contract, even for paths/methods no
// handler above recognizes.
const apiPrefix = "/api/"

// handleRoot is the catch-all registered on "/". It first rejects anything
// under apiPrefix that fell through (unknown /api/* path, or a
// registered /api/* path hit with the wrong HTTP method) with a JSON 404,
// then serves static frontend assets for genuine non-API paths.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, apiPrefix) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.handleStatic(w, r)
}

// handleStatic serves the embedded frontend assets. Paths that exist in the
// asset FS are served directly; unknown non-API paths fall back to
// index.html (SPA-style) so client-side routing works once the frontend
// slice adds it. Today's placeholder index.html makes the fallback a no-op
// in practice, but this is the intended long-term contract.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	if name == "" || name == "/" {
		name = "/index.html"
	}
	trimmed := name[1:] // fs.FS paths are never rooted with "/"

	if info, err := fs.Stat(s.assets, trimmed); err == nil && !info.IsDir() {
		http.ServeFileFS(w, r, s.assets, trimmed)
		return
	}
	http.ServeFileFS(w, r, s.assets, "index.html")
}

// Serve runs the HTTP server on ln, blocking until the server is closed
// (typically via Shutdown). It returns nil on a clean shutdown, matching
// net/http.Server.Serve's http.ErrServerClosed convention.
func (s *Server) Serve(ln net.Listener) error {
	s.httpServer = &http.Server{Handler: s}
	err := s.httpServer.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server, waiting for in-flight requests to
// finish or ctx to be done. Safe to call even if Serve has not been called
// yet (returns nil).
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}
