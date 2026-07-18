package cmd

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/webui"
)

// exploreURLResult is the --json output shape for "known explore".
type exploreURLResult struct {
	URL string `json:"url"`
}

// runExplore implements the "known explore" subcommand: it starts a
// localhost HTTP server (webui.Server) exposing a read-only JSON API and the
// embedded graph-explorer frontend, prints the listen URL, best-effort opens
// it in a browser, and serves until the command context is cancelled.
func runExplore(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("explore", flag.ContinueOnError)
	host := fs.String("host", "127.0.0.1", "host to bind the explorer server to")
	port := fs.Int("port", 0, "port to bind the explorer server to (0 = OS-assigned)")
	scope := fs.String("scope", "", "default scope pre-selected in the explorer UI")
	noBrowser := fs.Bool("no-browser", false, "do not open the explorer URL in a browser")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Resolve the default scope the same way search.go/list.go do, with a
	// final fallback to "root" since query.Engine.SearchText requires a
	// non-empty scope (mirrors resolveScope() in resolve.go).
	defaultScope := *scope
	if defaultScope == "" {
		defaultScope = app.Config.DefaultScope
	} else {
		defaultScope = app.ResolveScope(ctx, defaultScope)
	}
	if defaultScope == "" {
		defaultScope = model.RootScope
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("listen on %s:%d: %w", *host, *port, err)
	}

	srv := webui.New(app.Entries, app.Edges, app.Scopes, app.DB.Labels(), app.Engine, defaultScope)

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return fmt.Errorf("unexpected listener address type %T", ln.Addr())
	}
	url := fmt.Sprintf("http://%s/", net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port)))

	if app.Printer.json {
		app.Printer.printJSON(exploreURLResult{URL: url})
	} else {
		fmt.Fprintf(app.Printer.w, "Serving explorer at %s\n", url)
	}

	if !*noBrowser {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(app.Stderr, "warning: could not open browser: %v\n", err)
		}
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(app.Stderr, "warning: graceful shutdown failed: %v\n", err)
		}
		<-serveErr
		return nil
	case err := <-serveErr:
		return err
	}
}

// openBrowser attempts to open url in the user's default browser using a
// platform-specific launcher. Best-effort: the caller treats failure as a
// warning, never fatal.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
