package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

func press[T int | byte](u *UI, cc T) {
	u.Handle(inputEvent{num: byte(cc), val: 127})
	u.Handle(inputEvent{num: byte(cc), val: 0})
}

func turn[T int | byte](u *UI, cc T, delta int) {
	v := byte(delta)
	if delta < 0 {
		v = byte(128 + delta)
	}
	u.Handle(inputEvent{num: byte(cc), val: v})
}

func hold[T int | byte](u *UI, cc T, down bool) {
	v := byte(0)
	if down {
		v = 127
	}
	u.Handle(inputEvent{num: byte(cc), val: v})
}

// browseTo walks the browser to an effect with the D-pad and the jog wheel,
// the way a user would.
func browseTo(t *testing.T, u *UI, name string) {
	t.Helper()
	e := mustEffect(t, name)
	for i := 0; i < 40; i++ {
		press(u, push3.CCDPadLeft)
	}
	for u.cat.Categories[u.Snapshot().BrCat] != e.Category {
		press(u, push3.CCDPadRight)
	}
	for u.browserItems(u.Snapshot().BrCat)[u.Snapshot().BrItem].Name != name {
		turn(u, push3.CCJogWheel, 1)
	}
}

func newTestUI(t *testing.T) (*UI, *Chain) {
	ch, _ := newTestChain(t, "")
	u := newUI(ch, catalog(t))
	return u, ch
}

func TestAddEditRemoveFlow(t *testing.T) {
	u, ch := newTestUI(t)

	// empty chain: the first bottom button opens the browser
	press(u, push3.CCScreenBot1)
	if u.Snapshot().View != viewBrowser {
		t.Fatal("bottom 1 on an empty chain should open the browser")
	}
	browseTo(t, u, "Galactic")
	press(u, push3.CCScreenBot8) // ADD
	s := u.Snapshot()
	if s.View != viewPlugin || s.Slot != 0 || ch.Len() != 1 {
		t.Fatalf("after ADD: view %v slot %d len %d", s.View, s.Slot, ch.Len())
	}
	if !strings.Contains(s.Flash, "Galactic") {
		t.Fatalf("flash %q", s.Flash)
	}

	// encoder 2 moves parameter 2, Shift makes it finer
	before := ch.Params(0)[1].Value
	turn(u, push3.CCEncoder2, 10)
	mid := ch.Params(0)[1].Value
	hold(u, push3.CCShift, true)
	turn(u, push3.CCEncoder2, 10)
	hold(u, push3.CCShift, false)
	after := ch.Params(0)[1].Value
	if d := mid - before; d < 0.04 || d > 0.06 {
		t.Fatalf("coarse step %v", d)
	}
	if d := after - mid; d < 0.009 || d > 0.011 {
		t.Fatalf("fine step %v", d)
	}

	// Delete + touch encoder 2: back to default
	hold(u, push3.CCDelete, true)
	u.Handle(inputEvent{note: true, num: push3.NoteEncoder2Touch, val: 127})
	hold(u, push3.CCDelete, false)
	if p := ch.Params(0)[1]; p.Value != p.Default {
		t.Fatalf("not reset: %v vs %v", p.Value, p.Default)
	}

	// bypass from the plugin page
	press(u, push3.CCScreenTopN(topBypass))
	if s, _, _ := ch.Slot(0); !s.Bypass {
		t.Fatal("bypass button did not bypass")
	}

	// back, then Add appends a second effect
	press(u, push3.CCScreenTopN(topBack))
	press(u, push3.CCAdd)
	browseTo(t, u, "ToTape9")
	press(u, push3.CCJogPress)
	if ch.Len() != 2 || u.Snapshot().Slot != 1 {
		t.Fatalf("second add: len %d slot %d", ch.Len(), u.Snapshot().Slot)
	}

	// move it left: the page follows the effect
	press(u, push3.CCScreenTopN(topMoveLeft))
	if s := ch.Slots(); s[0].Name != "ToTape9" || u.Snapshot().Slot != 0 {
		t.Fatalf("move left: %v slot %d", s[0].Name, u.Snapshot().Slot)
	}

	// replace from the page
	press(u, push3.CCScreenTopN(topReplace))
	if s := u.Snapshot(); s.View != viewBrowser || s.BrMode != browseReplace {
		t.Fatal("replace did not open the browser")
	}
	if got := u.browserItems(u.Snapshot().BrCat)[u.Snapshot().BrItem].Name; got != "ToTape9" {
		t.Fatalf("replace should start on the current effect, got %s", got)
	}
	browseTo(t, u, "Mackity")
	press(u, push3.CCScreenBot8)
	if s := ch.Slots(); s[0].Name != "Mackity" || len(s) != 2 {
		t.Fatalf("replace: %+v", s)
	}

	// remove from the page: back to the chain, right away
	press(u, push3.CCScreenTopN(topRemove))
	if u.Snapshot().View != viewChain || ch.Len() != 1 || ch.Slots()[0].Name != "Galactic" {
		t.Fatalf("remove: view %v %+v", u.Snapshot().View, ch.Slots())
	}

	// chain view: top button toggles bypass, Delete + bottom removes
	press(u, push3.CCScreenTop1)
	if s, _, _ := ch.Slot(0); s.Bypass {
		t.Fatal("top 1 should un-bypass")
	}
	hold(u, push3.CCDelete, true)
	press(u, push3.CCScreenBot1)
	hold(u, push3.CCDelete, false)
	if ch.Len() != 0 {
		t.Fatal("Delete + bottom 1 should remove")
	}
}

func TestBrowserNavigation(t *testing.T) {
	u, _ := newTestUI(t)
	press(u, push3.CCAdd)
	cats := u.cat.Categories

	// encoder 1 is slowed down: 3 ticks are not a step, 4 are
	turn(u, push3.CCEncoder1, 3)
	if u.Snapshot().BrCat != 0 {
		t.Fatal("3 ticks moved the category")
	}
	turn(u, push3.CCEncoder1, 1)
	if u.Snapshot().BrCat != 1 {
		t.Fatal("4 ticks should move one category")
	}

	// jog past the end of a category goes into the next one
	press(u, push3.CCDPadLeft)
	n0 := len(u.InCat(0))
	for i := 0; i < n0; i++ {
		turn(u, push3.CCJogWheel, 1)
	}
	if s := u.Snapshot(); s.BrCat != 1 || s.BrItem != 0 {
		t.Fatalf("jog over the end: cat %d item %d", s.BrCat, s.BrItem)
	}
	turn(u, push3.CCJogWheel, -1)
	if s := u.Snapshot(); s.BrCat != 0 || s.BrItem != n0-1 {
		t.Fatalf("jog back: cat %d item %d", s.BrCat, s.BrItem)
	}

	// clamps at both ends of the catalog
	for i := 0; i < 50; i++ {
		press(u, push3.CCDPadRight)
	}
	if u.Snapshot().BrCat != len(cats)-1 {
		t.Fatal("did not clamp at the last category")
	}
	for i := 0; i < 1000; i++ {
		turn(u, push3.CCJogWheel, 5)
	}
	last := u.InCat(len(cats) - 1)
	if s := u.Snapshot(); s.BrItem != len(last)-1 {
		t.Fatalf("did not clamp at the last effect: %d", s.BrItem)
	}

	// BACK leaves without adding
	press(u, push3.CCScreenBot1)
	if u.Snapshot().View != viewChain || u.chain.Len() != 0 {
		t.Fatal("BACK should cancel")
	}
}

func mostParams(t testing.TB) Effect {
	var best Effect
	for _, e := range catalog(t).Effects {
		if e.NParams > best.NParams {
			best = e
		}
	}
	return best
}

func (u *UI) InCat(i int) []Effect { return u.browserItems(i) }

func TestFullChainRefusesAdd(t *testing.T) {
	u, ch := newTestUI(t)
	for ch.Len() < MaxSlots {
		ch.Insert(-1, mustEffect(t, "PurestGain").Index)
	}
	press(u, push3.CCAdd)
	s := u.Snapshot()
	if s.View != viewChain || !strings.Contains(s.Flash, "full") {
		t.Fatalf("full chain: view %v flash %q", s.View, s.Flash)
	}
}

func TestBanksAndWebRemoval(t *testing.T) {
	u, ch := newTestUI(t)
	big := mostParams(t)
	if big.NParams <= 8 {
		t.Fatalf("%s has only %d params", big.Name, big.NParams)
	}
	ch.Insert(-1, big.Index)
	n := ch.ParamCount(0)
	press(u, push3.CCScreenBot1)
	press(u, push3.CCPageRight)
	press(u, push3.CCPageRight) // clamps at the last bank
	if u.Snapshot().Bank != banks(n)-1 {
		t.Fatalf("bank %d", u.Snapshot().Bank)
	}
	// encoder 1 on bank 2 is parameter 9
	ch.SetParam(0, 8, 0.5)
	turn(u, push3.CCEncoder1, -20)
	if v := ch.Params(0)[8].Value; v > 0.41 {
		t.Fatalf("encoder 1 on bank 2 did not move parameter 9: %v", v)
	}
	press(u, push3.CCPageLeft)
	if u.Snapshot().Bank != 0 {
		t.Fatal("page left")
	}
	press(u, push3.CCScreenBotN(banks(n)-1))
	if u.Snapshot().Bank != banks(n)-1 {
		t.Fatal("bottom button did not pick the last bank")
	}

	// the web page removes the effect under the open page
	ch.Remove(0)
	if u.Snapshot().View != viewChain {
		t.Fatal("page of a removed slot should fall back to the chain")
	}
}

func TestLeaveDropsHeldModifiers(t *testing.T) {
	u, _ := newTestUI(t)
	hold(u, push3.CCDelete, true)
	hold(u, push3.CCShift, true)
	u.Leave()
	if s := u.Snapshot(); s.Delete || s.Shift {
		t.Fatal("Shift/Delete stuck after blur")
	}
}

func TestFlashExpires(t *testing.T) {
	u, ch := newTestUI(t)
	now := time.Now()
	u.now = func() time.Time { return now }
	for ch.Len() < MaxSlots {
		ch.Insert(-1, mustEffect(t, "PurestGain").Index)
	}
	press(u, push3.CCAdd)
	if u.Snapshot().Flash == "" {
		t.Fatal("no flash")
	}
	now = now.Add(3 * time.Second)
	if u.Snapshot().Flash != "" {
		t.Fatal("flash did not expire")
	}
}

func TestLEDs(t *testing.T) {
	u, ch := newTestUI(t)
	ch.Insert(-1, mustEffect(t, "Galactic").Index)
	ch.Insert(-1, mustEffect(t, "Air4").Index)
	ch.SetBypass(1, true)
	m := wantLEDs(u.Snapshot(), ch)
	if m[push3.CCScreenTopN(0)] != ledGreen || m[push3.CCScreenTopN(1)] != ledRed || m[push3.CCScreenTopN(2)] != 0 {
		t.Fatalf("chain top LEDs: %v %v %v", m[push3.CCScreenTopN(0)], m[push3.CCScreenTopN(1)], m[push3.CCScreenTopN(2)])
	}
	if m[push3.CCScreenBotN(2)] != ledDim {
		t.Fatal("the + ADD slot should be dim")
	}
	for cc := range m {
		found := false
		for _, l := range ledCCs {
			found = found || l == cc
		}
		if !found {
			t.Fatalf("LED %d lit but not in ledCCs, so it would never be turned off", cc)
		}
	}
}

func TestHomeStatus(t *testing.T) {
	u, ch := newTestUI(t)
	_ = u
	ch.Insert(-1, mustEffect(t, "Galactic").Index)
	st := homeStatus(ch, audioSnapshot{Connected: true, HasInput: true, Rate: 44100, Routed: true})
	if st[0] != "1 effect" || st[1] != "Input routed" {
		t.Fatalf("%v", st)
	}
	if st := homeStatus(ch, audioSnapshot{}); st[1] != "No audio bus" {
		t.Fatalf("%v", st)
	}
}

// TestRenderViews draws every view, checks it has the panel's size, and
// with AW_SHOTS=<dir> writes PNGs to look at.
func TestRenderViews(t *testing.T) {
	u, ch := newTestUI(t)
	for _, n := range []string{"Galactic", "Pressure5", "ToTape9", "Air4", mostParams(t).Name} {
		ch.Insert(-1, mustEffect(t, n).Index)
	}
	ch.SetBypass(2, true)
	ch.SetParam(0, 0, 0.8)
	ok := audioSnapshot{Connected: true, HasInput: true, Routed: true, Rate: 44100, Period: 128, DSPLoad: 0.07, PeakIn: 0.3, PeakOut: 0.6}

	dir := os.Getenv("AW_SHOTS")
	shot := func(name string, a audioSnapshot) {
		t.Helper()
		img := render(u.Snapshot(), ch, u.cat, u, a)
		if b := img.Bounds(); b.Dx() != screenW || b.Dy() != screenH {
			t.Fatalf("%s: %v", name, b)
		}
		if dir == "" {
			return
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, img)
	}

	shot("1-chain", ok)
	shot("2-chain-no-input", audioSnapshot{Connected: true, HasInput: true, Rate: 44100})
	hold(u, push3.CCDelete, true)
	shot("3-chain-delete", ok)
	hold(u, push3.CCDelete, false)
	press(u, push3.CCScreenBot1)
	shot("4-plugin-galactic", ok)
	press(u, push3.CCScreenTopN(topNextFX))
	press(u, push3.CCScreenTopN(topNextFX))
	shot("5-plugin-bypassed", ok)
	press(u, push3.CCScreenTopN(topNextFX))
	press(u, push3.CCScreenTopN(topNextFX))
	press(u, push3.CCPageRight)
	shot("6-plugin-bank2", ok)
	press(u, push3.CCScreenTopN(topBack))
	press(u, push3.CCAdd)
	browseTo(t, u, "Chamber2")
	shot("7-browser", ok)
	ch.Remove(4)
	ch.Remove(3)
	ch.Remove(2)
	ch.Remove(1)
	ch.Remove(0)
	press(u, push3.CCScreenBot1)
	shot("8-chain-empty", audioSnapshot{})
}
