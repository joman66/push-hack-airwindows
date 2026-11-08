package main

// aw.go - the one cgo file. Wraps dsp/awchain.h (the Airwindows registry
// and the effect chain) behind the Engine interface, so the rest of the hack
// is plain Go and its tests can use a fake engine.

/*
#cgo CFLAGS: -I${SRCDIR}/../dsp
#cgo LDFLAGS: ${SRCDIR}/../build/libairwin.a -lstdc++ -lm
#include <stdlib.h>
#include "awchain.h"
*/
import "C"

import (
	"errors"
	"sort"
	"unsafe"
)

// Effect is one entry of the Airwindows catalog.
type Effect struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Category string `json:"category"`
	What     string `json:"what"`
	NParams  int    `json:"nparams"`
}

// Stats is what the audio thread measured since the last call.
type Stats struct {
	WorstUs float64
	PeakIn  float64
	PeakOut float64
	Bad     int
}

// Engine is the effect chain. Positions are 0-based slots; effects are
// catalog indexes. Every method is safe from any goroutine; Process belongs
// to the audio goroutine.
type Engine interface {
	Len() int
	SlotEffect(pos int) int
	Insert(pos, effect int) error
	Replace(pos, effect int) error
	Remove(pos int) error
	Move(from, to int) error
	Clear()
	SetBypass(pos int, on bool)
	Bypass(pos int) bool
	ParamCount(pos int) int
	Param(pos, p int) float64
	SetParam(pos, p int, v float64)
	ParamName(pos, p int) string
	ParamDisplay(pos, p int) string
	SetGains(in, out float64)
	Process(buf []float32)
	Stats() Stats
}

// MaxSlots is how many effects the chain holds: one per encoder and
// screen button column.
const MaxSlots = C.AW_MAX_SLOTS

var errEngine = errors.New("airwindows: chain refused the change")

func goText(f func(buf *C.char, n C.int), n int) string {
	buf := (*C.char)(C.malloc(C.size_t(n)))
	defer C.free(unsafe.Pointer(buf))
	*buf = 0
	f(buf, C.int(n))
	return C.GoString(buf)
}

// loadCatalog reads every effect from the registry once, sorted by
// category, then name.
func loadCatalog() []Effect {
	n := int(C.aw_count())
	out := make([]Effect, 0, n)
	for i := 0; i < n; i++ {
		ci := C.int(i)
		out = append(out, Effect{
			Index:    i,
			Name:     goText(func(b *C.char, l C.int) { C.aw_name(ci, b, l) }, C.AW_TEXT),
			Category: goText(func(b *C.char, l C.int) { C.aw_category(ci, b, l) }, C.AW_TEXT),
			What:     goText(func(b *C.char, l C.int) { C.aw_what(ci, b, l) }, 512),
			NParams:  int(C.aw_nparams(ci)),
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Category != out[b].Category {
			return out[a].Category < out[b].Category
		}
		return out[a].Name < out[b].Name
	})
	return out
}

type cEngine struct{ c *C.aw_chain }

func newCEngine(sampleRate float64) *cEngine {
	return &cEngine{c: C.chain_new(C.float(sampleRate))}
}

func (e *cEngine) Free() { C.chain_free(e.c) }

func status(rc C.int) error {
	if rc != 0 {
		return errEngine
	}
	return nil
}

func (e *cEngine) Len() int               { return int(C.chain_len(e.c)) }
func (e *cEngine) SlotEffect(pos int) int { return int(C.chain_slot_effect(e.c, C.int(pos))) }
func (e *cEngine) Insert(pos, effect int) error {
	return status(C.chain_insert(e.c, C.int(pos), C.int(effect)))
}
func (e *cEngine) Replace(pos, effect int) error {
	return status(C.chain_replace(e.c, C.int(pos), C.int(effect)))
}
func (e *cEngine) Remove(pos int) error    { return status(C.chain_remove(e.c, C.int(pos))) }
func (e *cEngine) Move(from, to int) error { return status(C.chain_move(e.c, C.int(from), C.int(to))) }
func (e *cEngine) Clear()                  { C.chain_clear(e.c) }

func (e *cEngine) SetBypass(pos int, on bool) {
	v := 0
	if on {
		v = 1
	}
	C.chain_set_bypass(e.c, C.int(pos), C.int(v))
}
func (e *cEngine) Bypass(pos int) bool    { return C.chain_bypass(e.c, C.int(pos)) != 0 }
func (e *cEngine) ParamCount(pos int) int { return int(C.chain_param_count(e.c, C.int(pos))) }
func (e *cEngine) Param(pos, p int) float64 {
	return float64(C.chain_param_get(e.c, C.int(pos), C.int(p)))
}
func (e *cEngine) SetParam(pos, p int, v float64) {
	C.chain_param_set(e.c, C.int(pos), C.int(p), C.float(v))
}
func (e *cEngine) ParamName(pos, p int) string {
	return goText(func(b *C.char, l C.int) { C.chain_param_name(e.c, C.int(pos), C.int(p), b, l) }, C.AW_TEXT)
}
func (e *cEngine) ParamDisplay(pos, p int) string {
	return goText(func(b *C.char, l C.int) { C.chain_param_display(e.c, C.int(pos), C.int(p), b, l) }, C.AW_TEXT)
}
func (e *cEngine) SetGains(in, out float64) { C.chain_set_gains(e.c, C.float(in), C.float(out)) }

// Process runs the chain in place on interleaved stereo. No allocation:
// the slice's own memory goes to C for the length of the call.
func (e *cEngine) Process(buf []float32) {
	if len(buf) < 2 {
		return
	}
	C.chain_process(e.c, (*C.float)(unsafe.Pointer(&buf[0])), C.int(len(buf)/2))
}

func (e *cEngine) Stats() Stats {
	var w, pi, po C.float
	var bad C.int
	C.chain_stats(e.c, &w, &pi, &po, &bad)
	return Stats{WorstUs: float64(w), PeakIn: float64(pi), PeakOut: float64(po), Bad: int(bad)}
}
