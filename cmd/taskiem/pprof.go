package main

import (
	"log/slog"
	"net/http"
	"net/http/pprof"
)

// profiling serves Go's profiler under /debug/pprof/ on the metrics port
// when TASKIEM_PPROF=true (docs/performance.md). Off by default: profiles
// show the process's internals and taking one costs CPU. The metrics port
// is internal to the cluster; never expose it publicly with this on.
func profiling(mux *http.ServeMux, log *slog.Logger) {
	if !envBool("TASKIEM_PPROF", false) {
		return
	}
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	log.Warn("TASKIEM_PPROF=true: the profiler is served on the metrics port")
}
