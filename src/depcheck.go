package main

// depcheck.go - the on-screen editor needs push-manager and push-display.
// Say so in the log when either is missing, once per change, so a hack that
// draws nothing is easy to explain.

import (
	"log"
	"net/http"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/pmclient"
)

func runDependencyWatcher(pmURL string) {
	client := &pmclient.Client{Base: pmURL, HTTP: &http.Client{Timeout: 2 * time.Second}}
	last := ""
	for {
		state := "ok"
		if st, err := client.DisplayStatus(); err != nil {
			state = "push-manager unreachable"
		} else if !st.Connected {
			state = "push-display not connected"
		}
		if state != last {
			last = state
			switch state {
			case "ok":
				log.Printf("push-manager and push-display OK: the Push editor is available")
			case "push-manager unreachable":
				log.Printf("WARNING: push-manager not reachable at %s: the Push editor will not work until it runs", pmURL)
			default:
				log.Printf("WARNING: push-manager is up but push-display's framebuffer is not connected: install push-display for the Push editor")
			}
		}
		time.Sleep(30 * time.Second)
	}
}
