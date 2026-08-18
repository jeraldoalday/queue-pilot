package web

import (
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync"
)

// BuildInfo is what an instance can say about itself: which commit it was
// built from, whether that tree was clean, and when.
//
// It exists because the first question about a misbehaving deployment is
// "which version is that". Answering it from a deploy log means trusting
// that the log describes the process actually running; asking the process
// does not.
type BuildInfo struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Time     string `json:"time"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`
}

// buildInfoOnce caches the answer. debug.ReadBuildInfo walks the module
// graph, and this is stamped into the binary at link time — it cannot
// change while the process runs.
var buildInfoOnce = sync.OnceValue(readBuildInfo)

// readBuildInfo reads what the toolchain stamped into the binary.
//
// Go records the VCS revision automatically when building from a git
// checkout, so this needs no -ldflags and no generated file: a plain `go
// build` produces a binary that knows its own commit. The fields are
// absent when built from outside a repository (as `go test` does), which
// is reported as "unknown" rather than left blank — a blank field reads
// as a bug in the endpoint, where "unknown" reads as the fact it is.
func readBuildInfo() BuildInfo {
	info := BuildInfo{
		Version:  "unknown",
		Revision: "unknown",
		Time:     "unknown",
		Go:       runtime.Version(),
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}

	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.time":
			info.Time = setting.Value
		case "vcs.modified":
			// A binary built from a dirty tree does not correspond to any
			// commit, and saying so is the difference between a revision
			// that identifies the code and one that merely resembles it.
			info.Modified = setting.Value == "true"
		}
	}
	return info
}

// Build returns this binary's build information.
func Build() BuildInfo { return buildInfoOnce() }

// Short renders the build as one line, for a log or a status page.
func (b BuildInfo) Short() string {
	rev := b.Revision
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if b.Modified {
		rev += "-dirty"
	}
	return rev + " (" + b.Go + ")"
}

// handleVersion reports what this instance is running.
//
// It is deliberately unauthenticated and deliberately minimal. A deploy
// pipeline and an uptime check both need to read it without credentials,
// and the commit hash of a running service is not a secret — but nothing
// beyond identity belongs here, so there is no host name, no environment
// and no configuration in the response.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The answer is fixed for the life of the process, but caching it in a
	// proxy would defeat the point: this is asked precisely to find out
	// whether a deploy landed.
	w.Header().Set("Cache-Control", "no-store")

	if err := json.NewEncoder(w).Encode(Build()); err != nil {
		s.log.Error("handleVersion: encode", "error", err)
	}
}
