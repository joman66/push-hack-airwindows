package main

// ui.go - what the Push buttons and encoders do, as a plain state machine:
// Handle takes one button, encoder or touch event and changes the view or the
// chain. No drawing and no I/O here, so ui_test.go drives it directly.
//
// Three views:
//   chain    the 8 slots, IN > 1 > 2 > ... > OUT
//   browser  categories on the left, the effects of one category on the
//            right; adds a new effect or replaces one
//   plugin   one effect's parameters on the 8 encoders, 8 per bank

import (
	"fmt"
	"sync"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

type viewKind int

const (
	viewChain viewKind = iota
	viewBrowser
	viewPlugin
)

type browseMode int

const (
	browseInsert browseMode = iota
	browseReplace
)

// Plugin view top-row buttons, left to right.
const (
	topBack = iota
	topBypass
	topMoveLeft
	topMoveRight
	topReplace
	topRemove
	topPrevFX
	topNextFX
)

var pluginTopLabels = [8]string{"BACK", "BYPASS", "< MOVE", "MOVE >", "REPLACE", "REMOVE", "< FX", "FX >"}

const (
	paramStep     = 0.005 // per encoder tick; 200 ticks span a parameter
	paramStepFine = 0.001 // with Shift held
	gainStep      = 0.01
	catTicks      = 4 // encoder ticks per category step
	itemTicks     = 2 // encoder ticks per effect step
	flashFor      = 2500 * time.Millisecond
)

// inputEvent is one control event from Push's Live Port, channel 0.
type inputEvent struct {
	note bool // false: CC
	num  byte
	val  byte
}

// UI is the state of the on-screen controls.
type UI struct {
	chain *Chain
	cat   *Catalog
	now   func() time.Time

	mu    sync.Mutex
	view  viewKind
	slot  int // plugin view: which slot
	bank  int // plugin view: which 8 parameters
	shift bool
	del   bool

	brMode  browseMode
	brPos   int // insert position, or the slot to replace
	brCat   int // index into cat.Categories
	brItem  int // index into the category's effects
	brAccum [2]int

	flash      string
	flashUntil time.Time
	rev        uint64 // grows on every UI change, for redraws
}

func newUI(chain *Chain, cat *Catalog) *UI {
	return &UI{chain: chain, cat: cat, now: time.Now}
}

// uiSnapshot is a copy of the UI state for one frame.
type uiSnapshot struct {
	View   viewKind
	Slot   int
	Bank   int
	Shift  bool
	Delete bool
	BrMode browseMode
	BrPos  int
	BrCat  int
	BrItem int
	Flash  string
	Rev    uint64
}

func (u *UI) Snapshot() uiSnapshot {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.validateLocked()
	s := uiSnapshot{u.view, u.slot, u.bank, u.shift, u.del, u.brMode, u.brPos, u.brCat, u.brItem, "", u.rev}
	if u.now().Before(u.flashUntil) {
		s.Flash = u.flash
	}
	return s
}

// validateLocked keeps the view inside the chain after a change from the web
// page (a removed slot, a shorter chain).
func (u *UI) validateLocked() {
	n := u.chain.Len()
	if u.view == viewPlugin && u.slot >= n {
		u.view = viewChain
		u.rev++
	}
	if u.view == viewPlugin {
		if nb := banks(u.chain.ParamCount(u.slot)); u.bank >= nb {
			u.bank = 0
		}
	}
	if u.view == viewBrowser && u.brMode == browseReplace && u.brPos >= n {
		u.view = viewChain
		u.rev++
	}
}

func banks(nparams int) int {
	if nparams <= 0 {
		return 1
	}
	return (nparams + 7) / 8
}

func (u *UI) say(msg string) {
	u.flash = msg
	u.flashUntil = u.now().Add(flashFor)
}

// Handle applies one event. It reports whether anything changed.
func (u *UI) Handle(ev inputEvent) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.validateLocked()
	before := u.rev
	ver := u.chain.Version()
	if ev.note {
		u.handleNote(ev)
	} else {
		u.handleCC(ev)
	}
	if u.rev == before && u.chain.Version() != ver {
		u.rev++
	}
}

func (u *UI) handleNote(ev inputEvent) {
	// Delete held + touch an encoder: back to the effect's own default, as
	// Live does on Push.
	if u.view != viewPlugin || !u.del || ev.val == 0 {
		return
	}
	if ev.num >= push3.NoteEncoder1Touch && ev.num <= push3.NoteEncoder8Touch {
		p := u.bank*8 + int(ev.num-push3.NoteEncoder1Touch)
		u.chain.ResetParam(u.slot, p)
		u.rev++
	}
}

func (u *UI) handleCC(ev inputEvent) {
	cc, val := int(ev.num), ev.val
	pressed := val > 0

	switch cc {
	case push3.CCShift:
		u.shift = pressed
		u.rev++
		return
	case push3.CCDelete:
		u.del = pressed
		u.rev++
		return
	case push3.CCVolume:
		in, out := u.chain.Gains()
		step := gainStep
		if u.shift {
			step /= 5
		}
		u.chain.SetGains(in, out+float64(push3.DecodeRel(val))*step)
		return
	case push3.CCAdd:
		if pressed {
			u.openInsertLocked(-1)
		}
		return
	}

	switch u.view {
	case viewChain:
		u.chainCC(cc, val)
	case viewPlugin:
		u.pluginCC(cc, val)
	case viewBrowser:
		u.browserCC(cc, val)
	}
}

// ---- chain view ----

func (u *UI) chainCC(cc int, val byte) {
	if val == 0 {
		return
	}
	n := u.chain.Len()
	switch {
	case cc >= push3.CCScreenTop1 && cc <= push3.CCScreenTop8:
		i := cc - push3.CCScreenTop1
		if i >= n {
			return
		}
		if u.del {
			u.removeLocked(i)
			return
		}
		u.chain.ToggleBypass(i)
	case cc >= push3.CCScreenBot1 && cc <= push3.CCScreenBot8:
		i := cc - push3.CCScreenBot1
		switch {
		case i < n && u.del:
			u.removeLocked(i)
		case i < n:
			u.openPluginLocked(i)
		default:
			u.openInsertLocked(-1)
		}
	}
}

func (u *UI) removeLocked(i int) {
	s, _, ok := u.chain.Slot(i)
	if !ok {
		return
	}
	if err := u.chain.Remove(i); err != nil {
		u.say("Remove failed: " + err.Error())
	} else {
		u.say(fmt.Sprintf("Removed %s from slot %d", s.Name, i+1))
	}
	if u.view == viewPlugin {
		u.view = viewChain
	}
	u.rev++
}

func (u *UI) openPluginLocked(i int) {
	u.view = viewPlugin
	u.slot = i
	u.bank = 0
	u.rev++
}

// ---- plugin view ----

func (u *UI) pluginCC(cc int, val byte) {
	n := u.chain.Len()
	switch {
	case cc >= push3.CCEncoder1 && cc <= push3.CCEncoder8:
		p := u.bank*8 + cc - push3.CCEncoder1
		if u.del {
			u.chain.ResetParam(u.slot, p)
			return
		}
		step := paramStep
		if u.shift {
			step = paramStepFine
		}
		u.chain.NudgeParam(u.slot, p, float64(push3.DecodeRel(val))*step)
		return
	}
	if val == 0 {
		return
	}
	switch {
	case cc >= push3.CCScreenTop1 && cc <= push3.CCScreenTop8:
		switch cc - push3.CCScreenTop1 {
		case topBack:
			u.view = viewChain
		case topBypass:
			u.chain.ToggleBypass(u.slot)
		case topMoveLeft:
			if u.slot > 0 && u.chain.Move(u.slot, u.slot-1) == nil {
				u.slot--
			}
		case topMoveRight:
			if u.slot < n-1 && u.chain.Move(u.slot, u.slot+1) == nil {
				u.slot++
			}
		case topReplace:
			u.openReplaceLocked(u.slot)
		case topRemove:
			u.removeLocked(u.slot)
		case topPrevFX:
			u.stepSlotLocked(-1)
		case topNextFX:
			u.stepSlotLocked(1)
		}
		u.rev++
	case cc >= push3.CCScreenBot1 && cc <= push3.CCScreenBot8:
		if b := cc - push3.CCScreenBot1; b < banks(u.chain.ParamCount(u.slot)) {
			u.bank = b
			u.rev++
		}
	case cc == push3.CCPageLeft:
		if u.bank > 0 {
			u.bank--
			u.rev++
		}
	case cc == push3.CCPageRight:
		if u.bank < banks(u.chain.ParamCount(u.slot))-1 {
			u.bank++
			u.rev++
		}
	case cc == push3.CCDPadLeft:
		u.stepSlotLocked(-1)
	case cc == push3.CCDPadRight:
		u.stepSlotLocked(1)
	}
}

func (u *UI) stepSlotLocked(d int) {
	n := u.chain.Len()
	if s := u.slot + d; s >= 0 && s < n {
		u.slot = s
		u.bank = 0
		u.rev++
	}
}

// ---- browser ----

func (u *UI) openInsertLocked(pos int) {
	if u.chain.Len() >= MaxSlots {
		u.say(fmt.Sprintf("The chain is full (%d effects): remove one first", MaxSlots))
		u.rev++
		return
	}
	u.view = viewBrowser
	u.brMode = browseInsert
	u.brPos = pos
	u.brAccum = [2]int{}
	u.rev++
}

func (u *UI) openReplaceLocked(pos int) {
	u.view = viewBrowser
	u.brMode = browseReplace
	u.brPos = pos
	u.brAccum = [2]int{}
	if _, eff, ok := u.chain.Slot(pos); ok {
		u.selectEffectLocked(eff)
	}
	u.rev++
}

// selectEffectLocked points the browser at one effect.
func (u *UI) selectEffectLocked(eff Effect) {
	for ci, c := range u.cat.Categories {
		if c != eff.Category {
			continue
		}
		u.brCat = ci
		for ii, e := range u.cat.InCategory(c) {
			if e.Index == eff.Index {
				u.brItem = ii
			}
		}
	}
}

// BrowserItems is the effect list of the browser's current category.
func (u *UI) browserItems(cat int) []Effect {
	if cat < 0 || cat >= len(u.cat.Categories) {
		return nil
	}
	return u.cat.InCategory(u.cat.Categories[cat])
}

func (u *UI) moveCatLocked(d int) {
	nc := len(u.cat.Categories)
	if nc == 0 || d == 0 {
		return
	}
	c := u.brCat + d
	if c < 0 {
		c = 0
	}
	if c >= nc {
		c = nc - 1
	}
	if c != u.brCat {
		u.brCat = c
		u.brItem = 0
		u.rev++
	}
}

func (u *UI) moveItemLocked(d int) {
	items := u.browserItems(u.brCat)
	if len(items) == 0 || d == 0 {
		return
	}
	i := u.brItem + d
	// Past either end of a category: carry on into the next one, so the jog
	// wheel alone walks the whole catalog.
	for i >= len(items) && u.brCat < len(u.cat.Categories)-1 {
		i -= len(items)
		u.brCat++
		items = u.browserItems(u.brCat)
	}
	for i < 0 && u.brCat > 0 {
		u.brCat--
		items = u.browserItems(u.brCat)
		i += len(items)
	}
	if i < 0 {
		i = 0
	}
	if i >= len(items) {
		i = len(items) - 1
	}
	u.brItem = i
	u.rev++
}

// accumulate slows an encoder down for a list: Push sends faster deltas for
// a brisk turn, which would skip entries.
func (u *UI) accumulate(k int, delta, ticks int) int {
	u.brAccum[k] += delta
	steps := u.brAccum[k] / ticks
	u.brAccum[k] -= steps * ticks
	return steps
}

func (u *UI) browserCC(cc int, val byte) {
	switch cc {
	case push3.CCEncoder1:
		u.moveCatLocked(u.accumulate(0, push3.DecodeRel(val), catTicks))
		return
	case push3.CCEncoder2, push3.CCEncoder3, push3.CCEncoder4:
		u.moveItemLocked(u.accumulate(1, push3.DecodeRel(val), itemTicks))
		return
	case push3.CCJogWheel:
		if u.shift {
			u.moveCatLocked(push3.DecodeRel(val))
		} else {
			u.moveItemLocked(push3.DecodeRel(val))
		}
		return
	}
	if val == 0 {
		return
	}
	switch cc {
	case push3.CCDPadUp:
		u.moveItemLocked(-1)
	case push3.CCDPadDown:
		u.moveItemLocked(1)
	case push3.CCDPadLeft:
		u.moveCatLocked(-1)
	case push3.CCDPadRight:
		u.moveCatLocked(1)
	case push3.CCScreenBot1:
		u.cancelBrowseLocked()
	case push3.CCScreenBot8, push3.CCJogPress, push3.CCSelect, push3.CCDPadCenter:
		u.confirmBrowseLocked()
	}
}

func (u *UI) cancelBrowseLocked() {
	if u.brMode == browseReplace {
		u.view = viewPlugin
		u.slot = u.brPos
	} else {
		u.view = viewChain
	}
	u.rev++
}

// confirmBrowseLocked adds or swaps in the highlighted effect and opens its
// page. The change happens right away.
func (u *UI) confirmBrowseLocked() {
	items := u.browserItems(u.brCat)
	if u.brItem < 0 || u.brItem >= len(items) {
		return
	}
	eff := items[u.brItem]
	switch u.brMode {
	case browseReplace:
		if err := u.chain.Replace(u.brPos, eff.Index); err != nil {
			u.say("Replace failed: " + err.Error())
			u.rev++
			return
		}
		u.say(fmt.Sprintf("Slot %d is now %s", u.brPos+1, eff.Name))
		u.openPluginLocked(u.brPos)
	default:
		pos, err := u.chain.Insert(u.brPos, eff.Index)
		if err != nil {
			u.say("Add failed: " + err.Error())
			u.view = viewChain
			u.rev++
			return
		}
		u.say(fmt.Sprintf("Added %s to slot %d", eff.Name, pos+1))
		u.openPluginLocked(pos)
	}
}

// Leave resets the transient state when the app loses the surface: a held
// Shift or Delete must not stick.
func (u *UI) Leave() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.shift, u.del = false, false
	u.rev++
}
