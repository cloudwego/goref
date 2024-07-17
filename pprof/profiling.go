package pprof

import (
	"log"
	"net/http"
	"os"

	"github.com/cloudwego/goref/pkg/reexec"
)

const forkGoref = "fork_goref"

func init() {
	reexec.Register(forkGoref, func() {
		println("hello world")
		// attachCmd
	})
	if reexec.Init() {
		os.Exit(0)
	}
}

func init() {
	http.HandleFunc("/debug/pprof/reference", Reference)
}

// Reference responds with the pprof-formatted heap reference profile.
func Reference(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Set Content Type assuming StartCPUProfile will work,
	// because if it does it starts writing.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="profile"`)
	cmd := reexec.Command(forkGoref)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Panicf("failed to run goref command: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		log.Panicf("failed to wait goref command: %v", err)
	}
}
