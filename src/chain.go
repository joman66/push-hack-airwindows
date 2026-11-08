package main

// chain.go - the chain as the user sees it: which effects sit in which slot,
// their parameters and bypass, the gains, and the file that keeps them
// between restarts. Push and the web page both change the chain through
// here, so every change is saved and every view sees the same state.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const stateFileName = "airwindows-chain.json"

// Catalog is the sorted effect list plus a category index.
type Catalog struct {
	Effects    []Effect         // sorted by category, then name
	Categories []string         // sorted
	byCat      map[string][]int // category -> positions in Effects
	byIndex    map[int]int      // registry index -> position in Effects
	byName     map[string]int   // name -> position in Effects
}

func newCatalog(effects []Effect) *Catalog {
	c := &Catalog{Effects: effects, byCat: map[string][]int{}, byIndex: map[int]int{}, byName: map[string]int{}}
	for i, e := range effects {
		if _, ok := c.byCat[e.Category]; !ok {
			c.Categories = append(c.Categories, e.Category)
		}
		c.byCat[e.Category] = append(c.byCat[e.Category], i)
		c.byIndex[e.Index] = i
		c.byName[e.Name] = i
	}
	sort.Strings(c.Categories)
	return c
}

// InCategory lists the effects of one category, in display order.
func (c *Catalog) InCategory(cat string) []Effect {
	pos := c.byCat[cat]
	out := make([]Effect, len(pos))
	for i, p := range pos {
		out[i] = c.Effects[p]
	}
	return out
}

// ByIndex finds an effect by its registry index.
func (c *Catalog) ByIndex(idx int) (Effect, bool) {
	p, ok := c.byIndex[idx]
	if !ok {
		return Effect{}, false
	}
	return c.Effects[p], true
}

// ByName finds an effect by name.
func (c *Catalog) ByName(name string) (Effect, bool) {
	p, ok := c.byName[name]
	if !ok {
		return Effect{}, false
	}
	return c.Effects[p], true
}

// SlotState is one slot as saved and as shown.
type SlotState struct {
	Name     string    `json:"name"`
	Category string    `json:"category,omitempty"`
	Bypass   bool      `json:"bypass"`
	Params   []float64 `json:"params"`
}

// savedState is airwindows-chain.json.
type savedState struct {
	Slots   []SlotState `json:"slots"`
	InGain  float64     `json:"in_gain"`
	OutGain float64     `json:"out_gain"`
}

// Param is one parameter as the UI shows it.
type Param struct {
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Display string  `json:"display"`
	Default float64 `json:"default"`
}

// Chain owns the engine's layout. One mutex orders changes from Push and the
// web page; the engine itself guards the audio thread.
type Chain struct {
	eng Engine
	cat *Catalog

	mu       sync.Mutex
	defaults [][]float64 // per slot: parameter values when the effect was made
	inGain   float64
	outGain  float64
	version  uint64 // grows on every change, for redraws and the web page

	path    string
	saveReq chan struct{}
}

var (
	errFull     = errors.New("the chain is full")
	errNoEffect = errors.New("no such effect")
	errNoSlot   = errors.New("no such slot")
)

func newChain(eng Engine, cat *Catalog, dir string) *Chain {
	c := &Chain{eng: eng, cat: cat, inGain: 1, outGain: 1, saveReq: make(chan struct{}, 1)}
	if dir != "" {
		c.path = filepath.Join(dir, stateFileName)
	}
	eng.SetGains(1, 1)
	return c
}

func (c *Chain) changed() {
	c.version++
	select {
	case c.saveReq <- struct{}{}:
	default:
	}
}

// Version grows with every change.
func (c *Chain) Version() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// Len is the number of effects in the chain.
func (c *Chain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.eng.Len()
}

func (c *Chain) readDefaults(pos int) []float64 {
	n := c.eng.ParamCount(pos)
	d := make([]float64, n)
	for p := 0; p < n; p++ {
		d[p] = c.eng.Param(pos, p)
	}
	return d
}

// Insert adds an effect (registry index) at pos; pos < 0 or past the end
// appends. It returns the slot it landed in.
func (c *Chain) Insert(pos, effect int) (int, error) {
	if _, ok := c.cat.ByIndex(effect); !ok {
		return -1, errNoEffect
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.eng.Len()
	if n >= MaxSlots {
		return -1, errFull
	}
	if pos < 0 || pos > n {
		pos = n
	}
	if err := c.eng.Insert(pos, effect); err != nil {
		return -1, err
	}
	c.defaults = append(c.defaults, nil)
	copy(c.defaults[pos+1:], c.defaults[pos:])
	c.defaults[pos] = c.readDefaults(pos)
	c.changed()
	return pos, nil
}

// Replace swaps the effect in a slot for another. Bypass stays.
func (c *Chain) Replace(pos, effect int) error {
	if _, ok := c.cat.ByIndex(effect); !ok {
		return errNoEffect
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() {
		return errNoSlot
	}
	if err := c.eng.Replace(pos, effect); err != nil {
		return err
	}
	c.defaults[pos] = c.readDefaults(pos)
	c.changed()
	return nil
}

// Remove takes a slot out; the slots after it move up.
func (c *Chain) Remove(pos int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() {
		return errNoSlot
	}
	if err := c.eng.Remove(pos); err != nil {
		return err
	}
	c.defaults = append(c.defaults[:pos], c.defaults[pos+1:]...)
	c.changed()
	return nil
}

// Move puts slot from at position to.
func (c *Chain) Move(from, to int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.eng.Len()
	if from < 0 || from >= n || to < 0 || to >= n {
		return errNoSlot
	}
	if from == to {
		return nil
	}
	if err := c.eng.Move(from, to); err != nil {
		return err
	}
	d := c.defaults[from]
	c.defaults = append(c.defaults[:from], c.defaults[from+1:]...)
	c.defaults = append(c.defaults[:to], append([][]float64{d}, c.defaults[to:]...)...)
	c.changed()
	return nil
}

// SetBypass turns a slot off (true) or on.
func (c *Chain) SetBypass(pos int, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() {
		return
	}
	c.eng.SetBypass(pos, on)
	c.changed()
}

// ToggleBypass flips a slot's bypass.
func (c *Chain) ToggleBypass(pos int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() {
		return
	}
	c.eng.SetBypass(pos, !c.eng.Bypass(pos))
	c.changed()
}

// Slot reports one slot. ok is false for an empty slot.
func (c *Chain) Slot(pos int) (s SlotState, eff Effect, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slotLocked(pos)
}

func (c *Chain) slotLocked(pos int) (SlotState, Effect, bool) {
	if pos < 0 || pos >= c.eng.Len() {
		return SlotState{}, Effect{}, false
	}
	eff, _ := c.cat.ByIndex(c.eng.SlotEffect(pos))
	s := SlotState{Name: eff.Name, Category: eff.Category, Bypass: c.eng.Bypass(pos)}
	n := c.eng.ParamCount(pos)
	s.Params = make([]float64, n)
	for p := 0; p < n; p++ {
		s.Params[p] = c.eng.Param(pos, p)
	}
	return s, eff, true
}

// Slots reports every filled slot in order.
func (c *Chain) Slots() []SlotState {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.eng.Len()
	out := make([]SlotState, 0, n)
	for i := 0; i < n; i++ {
		s, _, _ := c.slotLocked(i)
		out = append(out, s)
	}
	return out
}

// ParamCount is how many parameters a slot's effect has.
func (c *Chain) ParamCount(pos int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() {
		return 0
	}
	return c.eng.ParamCount(pos)
}

// Params reports a slot's parameters with names and printed values.
func (c *Chain) Params(pos int) []Param {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() {
		return nil
	}
	n := c.eng.ParamCount(pos)
	out := make([]Param, n)
	for p := 0; p < n; p++ {
		out[p] = Param{
			Name:    c.eng.ParamName(pos, p),
			Value:   c.eng.Param(pos, p),
			Display: c.eng.ParamDisplay(pos, p),
		}
		if p < len(c.defaults[pos]) {
			out[p].Default = c.defaults[pos][p]
		}
	}
	return out
}

// SetParam writes a value in [0,1].
func (c *Chain) SetParam(pos, p int, v float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() || p < 0 || p >= c.eng.ParamCount(pos) {
		return
	}
	c.eng.SetParam(pos, p, clamp01(v))
	c.changed()
}

// NudgeParam adds d to a value and clamps it.
func (c *Chain) NudgeParam(pos, p int, d float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() || p < 0 || p >= c.eng.ParamCount(pos) {
		return
	}
	c.eng.SetParam(pos, p, clamp01(c.eng.Param(pos, p)+d))
	c.changed()
}

// ResetParam puts a value back to what the effect started with.
func (c *Chain) ResetParam(pos, p int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pos < 0 || pos >= c.eng.Len() || p < 0 || p >= len(c.defaults[pos]) {
		return
	}
	c.eng.SetParam(pos, p, c.defaults[pos][p])
	c.changed()
}

// Gains are linear, 0..2 (+6 dB).
func (c *Chain) Gains() (in, out float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inGain, c.outGain
}

func (c *Chain) SetGains(in, out float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inGain, c.outGain = clampGain(in), clampGain(out)
	c.eng.SetGains(c.inGain, c.outGain)
	c.changed()
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampGain(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 2 {
		return 2
	}
	return v
}

// Load reads airwindows-chain.json. A missing file is an empty chain. An
// effect that no longer exists is skipped and logged.
func (c *Chain) Load() error {
	if c.path == "" {
		return nil
	}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st savedState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("%s: %w", c.path, err)
	}
	c.apply(st)
	return nil
}

func (c *Chain) apply(st savedState) {
	c.mu.Lock()
	c.eng.Clear()
	c.defaults = nil
	c.mu.Unlock()
	for _, s := range st.Slots {
		eff, ok := c.cat.ByName(s.Name)
		if !ok {
			log.Printf("chain: %q is not in this Airwindows build, skipped", s.Name)
			continue
		}
		pos, err := c.Insert(-1, eff.Index)
		if err != nil {
			log.Printf("chain: %s: %v", s.Name, err)
			continue
		}
		for p, v := range s.Params {
			c.SetParam(pos, p, v)
		}
		c.SetBypass(pos, s.Bypass)
	}
	in, out := st.InGain, st.OutGain
	if in == 0 && out == 0 { // an old or hand-written file without gains
		in, out = 1, 1
	}
	c.SetGains(in, out)
}

func (c *Chain) snapshot() savedState {
	in, out := c.Gains()
	return savedState{Slots: c.Slots(), InGain: in, OutGain: out}
}

// Save writes the file now, through a temp file so a crash never leaves
// half a file.
func (c *Chain) Save() error {
	if c.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(c.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// runSaver writes the file at most once a second while things change, and
// once more on shutdown.
func (c *Chain) runSaver(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			if err := c.Save(); err != nil {
				log.Printf("chain: save: %v", err)
			}
			return
		case <-c.saveReq:
		}
		select {
		case <-stop:
		case <-time.After(time.Second):
		}
		if err := c.Save(); err != nil {
			log.Printf("chain: save: %v", err)
		}
	}
}
