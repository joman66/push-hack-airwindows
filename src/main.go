// airwindows - an Airwindows effect chain for Push 3, as a Push Hack app.
//
// Audio from Live comes in over Ableton Link Audio (an input of this hack on
// push-audio-loopback's routing matrix), runs through up to 8 Airwindows
// effects, and goes back to Live as the Link channel "Airwindows". Push Hack
// Home opens the on-screen editor: the chain, a category browser, and a page
// of parameters for each effect. A web page shows the same chain.
//
// A catalog install only passes "-config <hack.json>", so everything else
// comes from files next to hack.json.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsaseq"
	"github.com/federico-pepe/ableton-push-hack/core/pmclient"
	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

const defaultPushManagerURL = "http://localhost:7701"

var buildVersion = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		os.Stdout.WriteString(buildVersion + "\n")
		return
	}
	// The init.d service only restarts this process on a service restart, not
	// on a crash. Run the real work in a child and restart it if the C++ side
	// ever takes the process down.
	if os.Getenv("AIRWINDOWS_SUPERVISED") != "1" {
		runSupervisor()
		return
	}
	runSupervised()
}

func runSupervisor() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	backoff := time.Second
	for {
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		cmd.Env = append(os.Environ(), "AIRWINDOWS_SUPERVISED=1")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("start child: %v, retry in %v", err, backoff)
			time.Sleep(backoff)
			continue
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		start := time.Now()
		select {
		case sig := <-sigCh:
			// Forward, or "stop" would leave the child running.
			_ = cmd.Process.Signal(sig)
			<-done
			return
		case err := <-done:
			ran := time.Since(start)
			log.Printf("child exited after %v: %v", ran.Round(time.Second), err)
			if ran > 30*time.Second {
				backoff = time.Second
			}
		}
		log.Printf("restarting in %v", backoff)
		time.Sleep(backoff)
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func runSupervised() {
	configPath := flag.String("config", "hack.json", "path to hack.json")
	flag.Parse()
	hackDir, err := filepath.Abs(filepath.Dir(*configPath))
	if err != nil {
		log.Fatalf("hack dir: %v", err)
	}
	webPort := resolveWebPort(*configPath)
	pmURL := defaultPushManagerURL
	if v := os.Getenv("PUSH_MANAGER_URL"); v != "" {
		pmURL = v
	}

	// No /dev/snd until the cold-boot USB window has passed (core/alsaseq).
	alsaseq.WaitForBootSettle()

	cat := newCatalog(loadCatalog())
	log.Printf("airwindows %s: %d effects in %d categories", buildVersion, len(cat.Effects), len(cat.Categories))

	eng := newCEngine(dspRate)
	defer eng.Free()
	chain := newChain(eng, cat, hackDir)
	if err := chain.Load(); err != nil {
		log.Printf("loading chain: %v (starting empty)", err)
	}
	log.Printf("chain: %d effects", chain.Len())

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("%v: stopping", <-sigCh)
		close(stop)
	}()

	audio := &audioStatus{}
	audioDone := make(chan struct{})
	go func() {
		runAudio(eng, audio, stop)
		close(audioDone)
	}()
	saverDone := make(chan struct{})
	go func() {
		chain.runSaver(stop)
		close(saverDone)
	}()

	ui := newUI(chain, cat)
	surf := &surface{pmURL: pmURL, ui: ui, chain: chain, cat: cat, audio: audio, kick: make(chan struct{}, 1)}
	events := make(chan inputEvent, 128)
	go runControlInput(events, stop)
	go surf.runEvents(events, stop)
	go surf.runDisplay(stop)
	go runDependencyWatcher(pmURL)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-stop; cancel() }()
	go func() {
		// Live keeps Play and Record while Airwindows has the surface.
		err := newPMClient(pmURL).RunSurface(ctx, []int{push3.CCPlay, push3.CCRecord}, pmclient.SurfaceHandler{
			OnFocus: surf.onFocus,
			OnBlur:  surf.onBlur,
		})
		if errors.Is(err, pmclient.ErrNoSwitcher) {
			log.Printf("push-manager has no Push Hack Home: update Push Hack to open Airwindows on Push (the web page still works)")
		}
	}()

	go runWebServer(webPort, chain, cat, audio, surf, stop)

	<-stop
	<-audioDone
	<-saverDone
	surf.leds.clear(pmURL)
	log.Printf("stopped")
}
