package main

// audio.go - the render loop. Audio comes in from a Link channel that the
// user routes to this hack's "Input" on push-audio-loopback's page, runs
// through the chain, and goes back out as this hack's own Link channel
// ("Airwindows"). The loop never stops while the bus is down: Read gives
// silence and Write sleeps one block, so the pace stays right (see
// "Audio from hacks" in ableton-push-hack's CLAUDE.md).

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/audiobus"
)

const (
	busName      = "Airwindows"
	busInputName = "Input"
	// Link inputs are always 44100 Hz stereo, so the effects run at that rate.
	dspRate       = 44100
	defaultPeriod = 128
)

// audioStatus is what the screen and the web page show about the bus.
type audioStatus struct {
	mu        sync.Mutex
	connected bool
	hasInput  bool // the server made our input (a new enough push-audio-loopback)
	routed    bool // real input frames arrived in the last second
	rate      int
	period    int
	dspLoad   float64 // worst block time / block length, last second
	peakIn    float64
	peakOut   float64
	bad       int
}

type audioSnapshot struct {
	Connected bool    `json:"connected"`
	HasInput  bool    `json:"has_input"`
	Routed    bool    `json:"routed"`
	Rate      int     `json:"rate"`
	Period    int     `json:"period"`
	DSPLoad   float64 `json:"dsp_load"`
	PeakIn    float64 `json:"peak_in"`
	PeakOut   float64 `json:"peak_out"`
	Bad       int     `json:"bad_samples"`
}

func (a *audioStatus) get() audioSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return audioSnapshot{a.connected, a.hasInput, a.routed, a.rate, a.period, a.dspLoad, a.peakIn, a.peakOut, a.bad}
}

// Message is the one line the screen shows when something needs the user.
// Empty when all is well.
func (s audioSnapshot) Message() string {
	switch {
	case !s.Connected:
		return "No audio bus: install and start Audio Loopback"
	case !s.HasInput:
		return "Audio Loopback is too old for inputs: update it"
	case s.Rate != 0 && s.Rate != dspRate:
		return "Audio Loopback is not in Link mode: switch it to Link"
	case !s.Routed:
		return "No input: route a Live track to Airwindows > Input"
	}
	return ""
}

// busClient is the open bus connection, for status only.
var busClient atomic.Pointer[audiobus.Client]

// runAudio renders until stop closes.
func runAudio(eng Engine, st *audioStatus, stop <-chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cl := audiobus.OpenWithInputs(audiobus.SocketPath(audiobus.DefaultDir), busName, -1, []string{busInputName})
	busClient.Store(cl)
	defer cl.Close()

	period := defaultPeriod
	buf := make([]float32, 2*period)
	var gen uint64
	realFrames := 0
	lastReport := time.Now()

	for {
		select {
		case <-stop:
			return
		default:
		}

		if g := cl.Generation(); g != gen {
			gen = g
			if f := cl.Format(); f.Period > 0 && f.Period != period {
				period = f.Period
				buf = make([]float32, 2*period)
			}
		}

		n, _ := cl.Read(0, buf) // silence when not connected or not routed
		realFrames += n
		eng.Process(buf)
		_ = cl.Write(buf) // waits for the mixer; sleeps one block without one

		if time.Since(lastReport) >= time.Second {
			s := eng.Stats()
			f := cl.Format()
			blockUs := float64(period) / dspRate * 1e6
			st.mu.Lock()
			st.connected = cl.Connected()
			st.hasInput = len(cl.Inputs()) > 0
			st.routed = realFrames > 0
			st.rate, st.period = f.Rate, period
			st.dspLoad = s.WorstUs / blockUs
			st.peakIn, st.peakOut, st.bad = s.PeakIn, s.PeakOut, s.Bad
			st.mu.Unlock()
			realFrames = 0
			lastReport = time.Now()
		}
	}
}
