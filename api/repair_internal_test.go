package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/runfiles"
)

// The repair worker gets the server's run file store, so a step whose
// connector keeps or reads run files works there as on a queue worker.
func TestRepairWorkerHasRunFiles(t *testing.T) {
	files := &runfiles.Service{}
	s := &Server{RunFiles: files}
	if w := s.repairWorker(); w.RunFiles != files {
		t.Fatalf("repair worker RunFiles = %p, want %p", w.RunFiles, files)
	}
}
