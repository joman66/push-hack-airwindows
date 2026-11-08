package main

// surface.go - Airwindows as an app of Push Hack Home. Home gives it the
// screen, buttons and encoders (focus) and takes them back (blur);
// push-manager switches the display and the MIDI intercept. This file only
// draws between focus and blur, keeps the LEDs in step, and feeds control
// events from Push's Live Port to the UI.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsaseq"
	"github.com/federico-pepe/ableton-push-hack/core/pmclient"
	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

const pmApp = "airwindows" // hack.json id

var focused atomic.Bool

func newPMClient(pmURL string) *pmclient.Client {
	c := pmclient.New(pmURL)
	c.App = pmApp
	return c
}

// ---- MIDI control input ----

// pushLive is Push 3's Live Port; its ALSA client number moves with the
// sound cards, so it is looked up again before each subscribe.
var pushLive alsaseq.Push3

type controlHandler struct{ out chan<- inputEvent }

func (h controlHandler) Fixed(evType uint8, src alsaseq.Addr, data []byte) {
	if !focused.Load() || !pushLive.IsLivePort(src) {
		return
	}
	// Push's own buttons and encoders are on channel 0. Other channels carry
	// the pads' MPE stream, whose CC 74 would read as encoder 4.
	if data[0]&0x0F != 0 {
		return
	}
	var ev inputEvent
	switch evType {
	case alsaseq.EvController:
		ev = inputEvent{
			num: byte(binary.LittleEndian.Uint32(data[4:]) & 0x7F),
			val: byte(binary.LittleEndian.Uint32(data[8:]) & 0x7F),
		}
	case alsaseq.EvNoteOn, alsaseq.EvNoteOff:
		vel := data[2]
		if evType == alsaseq.EvNoteOff {
			vel = 0
		}
		ev = inputEvent{note: true, num: data[1], val: vel}
	default:
		return
	}
	select {
	case h.out <- ev:
	default:
		log.Printf("control queue full, dropped event %+v", ev)
	}
}

func (controlHandler) VarLen(uint8, alsaseq.Addr, []byte) {}

// runControlInput keeps one ALSA port subscribed to Push's Live Port.
func runControlInput(out chan<- inputEvent, stop <-chan struct{}) {
	var seq *alsaseq.Client
	subscribed := map[alsaseq.Addr]bool{}
	lastErr := ""
	logOnce := func(msg string) { // the same failure every 2 s is noise
		if msg != lastErr {
			lastErr = msg
			log.Print(msg)
		}
	}
	defer func() {
		if seq != nil {
			seq.Close()
		}
	}()
	for {
		if seq == nil {
			s, err := alsaseq.Open()
			if err == nil {
				_, err = s.CreatePort("Airwindows Control", alsaseq.CapWrite|alsaseq.CapSubsWrite,
					alsaseq.PortTypeMidi|alsaseq.PortTypeApp)
				if err != nil {
					s.Close()
				}
			}
			if err != nil {
				logOnce(fmt.Sprintf("control input: %v, will retry", err))
			} else {
				seq = s
				go func(s *alsaseq.Client) {
					if err := s.ReadLoop(controlHandler{out: out}); err != nil {
						log.Printf("control input read loop ended: %v", err)
					}
				}(s)
			}
		}
		if seq != nil {
			if a := pushLive.Refresh(); !subscribed[a] {
				if err := seq.Subscribe(a); err != nil {
					logOnce(fmt.Sprintf("control input: subscribe to Push %v: %v, will retry", a, err))
				} else {
					subscribed[a] = true
					lastErr = ""
					log.Printf("control input: subscribed to Push Live Port %v", a)
				}
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// ---- LEDs ----

var ledHTTP = &http.Client{Timeout: 2 * time.Second}

func postLED(pmURL, body string) {
	req, err := http.NewRequest(http.MethodPost, pmURL+"/api/midi/led", bytes.NewBufferString(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := ledHTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

func ledIdx(name string) byte {
	idx, ok := push3.ColorByName(name)
	if !ok {
		panic("leds: unknown palette color " + name)
	}
	return idx
}

var (
	ledOff   = byte(0)
	ledWhite = ledIdx("white")
	ledDim   = ledIdx("dgray")
	ledGreen = ledIdx("green")
	ledRed   = ledIdx("red")
	ledAmber = ledIdx("orange")
)

// ledCCs are every button LED this app may light. Everything not lit by
// wantLEDs is turned off, so nothing from Live stays lit under the app.
var ledCCs = func() []byte {
	var out []byte
	for i := 0; i < 8; i++ {
		out = append(out, push3.CCScreenTopN(i), push3.CCScreenBotN(i))
	}
	return append(out, push3.CCAdd, push3.CCDelete, push3.CCShift, push3.CCPageLeft, push3.CCPageRight,
		push3.CCDPadLeft, push3.CCDPadRight, push3.CCDPadUp, push3.CCDPadDown, push3.CCSelect)
}()

// wantLEDs is the LED state for a frame. Pure, for tests.
func wantLEDs(u uiSnapshot, ch *Chain) map[byte]byte {
	m := map[byte]byte{}
	n := ch.Len()
	if n < MaxSlots {
		m[push3.CCAdd] = ledWhite
	}
	m[push3.CCShift] = ledDim
	switch u.View {
	case viewChain:
		m[push3.CCDelete] = ledDim
		for i := 0; i < 8; i++ {
			s, _, ok := ch.Slot(i)
			switch {
			case ok && u.Delete:
				m[push3.CCScreenTopN(i)] = ledRed
				m[push3.CCScreenBotN(i)] = ledRed
			case ok:
				if s.Bypass {
					m[push3.CCScreenTopN(i)] = ledRed
				} else {
					m[push3.CCScreenTopN(i)] = ledGreen
				}
				m[push3.CCScreenBotN(i)] = ledWhite
			case i == n:
				m[push3.CCScreenBotN(i)] = ledDim
			}
		}
	case viewPlugin:
		m[push3.CCDelete] = ledDim
		s, _, _ := ch.Slot(u.Slot)
		m[push3.CCScreenTopN(topBack)] = ledWhite
		if s.Bypass {
			m[push3.CCScreenTopN(topBypass)] = ledRed
		} else {
			m[push3.CCScreenTopN(topBypass)] = ledGreen
		}
		if u.Slot > 0 {
			m[push3.CCScreenTopN(topMoveLeft)] = ledDim
			m[push3.CCScreenTopN(topPrevFX)] = ledDim
			m[push3.CCDPadLeft] = ledDim
		}
		if u.Slot < n-1 {
			m[push3.CCScreenTopN(topMoveRight)] = ledDim
			m[push3.CCScreenTopN(topNextFX)] = ledDim
			m[push3.CCDPadRight] = ledDim
		}
		m[push3.CCScreenTopN(topReplace)] = ledWhite
		m[push3.CCScreenTopN(topRemove)] = ledRed
		nb := banks(ch.ParamCount(u.Slot))
		if nb > 1 {
			for b := 0; b < nb && b < 8; b++ {
				m[push3.CCScreenBotN(b)] = ledDim
			}
			m[push3.CCScreenBotN(u.Bank)] = ledWhite
			if u.Bank > 0 {
				m[push3.CCPageLeft] = ledWhite
			}
			if u.Bank < nb-1 {
				m[push3.CCPageRight] = ledWhite
			}
		}
	case viewBrowser:
		m[push3.CCScreenBotN(0)] = ledWhite
		m[push3.CCScreenBotN(7)] = ledGreen
		m[push3.CCSelect] = ledAmber
		for _, cc := range []byte{push3.CCDPadLeft, push3.CCDPadRight, push3.CCDPadUp, push3.CCDPadDown} {
			m[cc] = ledDim
		}
	}
	return m
}

// ledState sends only the LEDs that changed since the last frame.
type ledState struct {
	mu   sync.Mutex
	last map[byte]byte
}

func (l *ledState) reset() {
	l.mu.Lock()
	l.last = nil
	l.mu.Unlock()
}

func (l *ledState) sync(pmURL string, want map[byte]byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[byte]byte{}
		for _, cc := range ledCCs {
			l.last[cc] = 255 // unknown: send everything once
		}
	}
	for _, cc := range ledCCs {
		v := want[cc]
		if l.last[cc] == v {
			continue
		}
		postLED(pmURL, fmt.Sprintf(`{"type":"cc","channel":0,"cc":%d,"value":%d}`, cc, v))
		l.last[cc] = v
	}
}

func (l *ledState) clear(pmURL string) {
	l.sync(pmURL, map[byte]byte{})
	l.reset()
}

// clearPadLEDs turns the 64 pads off: Home lights a row for its entries and
// this app does not use the pads. Three passes, as Braids does, since Home
// may still be drawing its last frame.
func clearPadLEDs(pmURL string) {
	for _, d := range []time.Duration{0, 300 * time.Millisecond, 900 * time.Millisecond} {
		time.Sleep(d)
		for row := 0; row < 8; row++ {
			for col := 0; col < 8; col++ {
				postLED(pmURL, fmt.Sprintf(`{"type":"note","channel":0,"note":%d,"velocity":0}`, push3.PadNote(col, row)))
			}
		}
	}
}

// ---- display loop ----

type surface struct {
	pmURL string
	ui    *UI
	chain *Chain
	cat   *Catalog
	audio *audioStatus
	leds  ledState
	kick  chan struct{} // redraw now
}

func (s *surface) redraw() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *surface) onFocus() {
	focused.Store(true)
	s.leds.reset()
	go clearPadLEDs(s.pmURL)
	s.redraw()
	log.Printf("focus: Airwindows has the surface")
}

func (s *surface) onBlur() {
	focused.Store(false)
	s.ui.Leave()
	s.leds.clear(s.pmURL)
	log.Printf("blur: the surface is back with Home or Live")
}

// runEvents applies control events to the UI and asks for a redraw.
func (s *surface) runEvents(in <-chan inputEvent, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case ev := <-in:
			s.ui.Handle(ev)
			s.redraw()
		}
	}
}

// runDisplay draws on every change, and at 8 fps for the meters, only while
// the app has the surface. It also keeps Home's two status lines current.
func (s *surface) runDisplay(stop <-chan struct{}) {
	client := newPMClient(s.pmURL)
	tick := time.NewTicker(125 * time.Millisecond)
	defer tick.Stop()
	var lastStatus [2]string
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		case <-s.kick:
		}

		a := s.audio.get()
		st := homeStatus(s.chain, a)
		if st != lastStatus {
			if err := client.SurfaceStatus(st[0], st[1]); err == nil {
				lastStatus = st
			}
		}

		if !focused.Load() {
			continue
		}
		u := s.ui.Snapshot()
		s.leds.sync(s.pmURL, wantLEDs(u, s.chain))
		if err := client.Push(render(u, s.chain, s.cat, s.ui, a)); err != nil {
			log.Printf("display: push frame: %v", err)
		}
	}
}

// homeStatus is the two lines on Airwindows' card in Push Hack Home.
func homeStatus(ch *Chain, a audioSnapshot) [2]string {
	n := ch.Len()
	line1 := fmt.Sprintf("%d effects", n)
	if n == 1 {
		line1 = "1 effect"
	}
	line2 := "Input routed"
	if m := a.Message(); m != "" {
		switch {
		case !a.Connected:
			line2 = "No audio bus"
		case !a.Routed:
			line2 = "No input"
		default:
			line2 = "Check Audio Loopback"
		}
	}
	return [2]string{line1, line2}
}
