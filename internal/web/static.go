package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// staticFS embeds the CSS and vendored JavaScript the pages load.
//
// These used to be served from the working directory with
// http.Dir("internal/web/static"), which quietly made the source tree a
// runtime dependency: the binary only rendered a styled page when started
// from the repository root. Anywhere else — a container with just the
// binary copied in, a systemd unit with a different WorkingDirectory, `go
// build` output run from a different folder — every page loaded with no
// stylesheet and no htmx, so the app looked broken and its polling pages
// stopped updating, with nothing in the logs beyond a pair of 404s.
//
// Embedding makes the README's "one binary" claim true. It also means an
// asset can never be half-deployed: it ships in the same artifact as the
// code that references it.
//
//go:embed static
var staticFS embed.FS

// staticMaxAge is how long a browser may reuse an asset without asking.
//
// It is short on purpose. The URLs here are unversioned — /static/css/app.css
// is the same URL after every rebuild — so a long max-age would leave a
// returning visitor on last week's stylesheet with no way to tell them
// otherwise. An hour keeps repeat page loads off the network for the
// length of a working session while bounding how stale a deploy can leave
// anyone.
const staticMaxAge = "public, max-age=3600"

// staticHandler serves the embedded assets under /static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Only reachable if the embed directive above stops matching,
		// which is a build-time mistake rather than a runtime condition.
		panic("web: static assets are not embedded: " + err.Error())
	}

	files := http.FileServerFS(noDirFS{sub})
	return http.StripPrefix("/static/", cacheControl(files))
}

// noDirFS hides directories from the file server.
//
// http.FileServerFS renders an HTML index for any directory without an
// index.html, so /static/css/ answered with a browsable listing of the
// asset tree. Nothing links to one, it is not something anyone asked to
// publish, and an index of what a server holds is the first thing a probe
// looks for. Refusing to open a directory turns those requests into the
// 404 they should have been.
type noDirFS struct{ fs.FS }

func (n noDirFS) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

// cacheControl stamps the max-age on responses that carry an asset.
//
// It leaves 404s alone: caching "this file does not exist" for an hour
// turns a missed asset into an hour of a broken page even after the file
// is deployed.
func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheHeaderWriter{ResponseWriter: w}, r)
	})
}

// cacheHeaderWriter sets Cache-Control at the moment the status is known,
// which is the only point at which a hit can be told from a miss.
type cacheHeaderWriter struct {
	http.ResponseWriter
	wrote bool
}

func (c *cacheHeaderWriter) WriteHeader(code int) {
	if !c.wrote {
		c.wrote = true
		if code == http.StatusOK || code == http.StatusNotModified {
			c.Header().Set("Cache-Control", staticMaxAge)
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *cacheHeaderWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

func (c *cacheHeaderWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// StaticAssets lists every embedded asset path, relative to /static/. It
// exists so a test can assert the files the templates reference are
// actually in the binary, rather than discovering it from a blank page.
func StaticAssets() ([]string, error) {
	var paths []string
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, strings.TrimPrefix(path, "static/"))
		}
		return nil
	})
	return paths, err
}
