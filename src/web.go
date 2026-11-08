package main

// web.go - the hack's web page (hack.json web_ui) and its JSON API. Changes
// from here go through the same Chain as the Push editor and redraw it.

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed ui/index.html
var uiFiles embed.FS

type stateResponse struct {
	Version uint64        `json:"version"`
	Slots   []slotJSON    `json:"slots"`
	InGain  float64       `json:"in_gain"`
	OutGain float64       `json:"out_gain"`
	Audio   audioSnapshot `json:"audio"`
	Message string        `json:"message"`
	Max     int           `json:"max_slots"`
}

type slotJSON struct {
	Name     string  `json:"name"`
	Category string  `json:"category"`
	What     string  `json:"what"`
	Bypass   bool    `json:"bypass"`
	Params   []Param `json:"params"`
}

type chainRequest struct {
	Op     string `json:"op"` // insert, replace, remove, move, bypass, clear
	Pos    int    `json:"pos"`
	To     int    `json:"to"`
	Name   string `json:"name"`
	Bypass bool   `json:"bypass"`
}

type paramRequest struct {
	Pos   int     `json:"pos"`
	Index int     `json:"index"`
	Value float64 `json:"value"`
	Reset bool    `json:"reset"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func buildState(ch *Chain, audio *audioStatus) stateResponse {
	a := audio.get()
	in, out := ch.Gains()
	r := stateResponse{Version: ch.Version(), InGain: in, OutGain: out, Audio: a, Message: a.Message(), Max: MaxSlots}
	for i := 0; i < ch.Len(); i++ {
		s, eff, ok := ch.Slot(i)
		if !ok {
			break
		}
		r.Slots = append(r.Slots, slotJSON{Name: s.Name, Category: s.Category, What: eff.What, Bypass: s.Bypass, Params: ch.Params(i)})
	}
	return r
}

func applyChain(ch *Chain, cat *Catalog, rq chainRequest) error {
	switch rq.Op {
	case "insert", "replace":
		eff, ok := cat.ByName(rq.Name)
		if !ok {
			return fmt.Errorf("no effect named %q", rq.Name)
		}
		if rq.Op == "replace" {
			return ch.Replace(rq.Pos, eff.Index)
		}
		_, err := ch.Insert(rq.Pos, eff.Index)
		return err
	case "remove":
		return ch.Remove(rq.Pos)
	case "move":
		return ch.Move(rq.Pos, rq.To)
	case "bypass":
		if rq.Pos < 0 || rq.Pos >= ch.Len() {
			return errNoSlot
		}
		ch.SetBypass(rq.Pos, rq.Bypass)
		return nil
	case "clear":
		for ch.Len() > 0 {
			if err := ch.Remove(ch.Len() - 1); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown op %q", rq.Op)
}

func newWebMux(ch *Chain, cat *Catalog, audio *audioStatus, surf *surface) *http.ServeMux {
	mux := http.NewServeMux()
	static, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("GET /", http.FileServer(http.FS(static)))

	redraw := func() {
		if surf != nil {
			surf.redraw()
		}
	}

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"hack_version": buildVersion, "effects": len(cat.Effects), "chain": ch.Len()})
	})
	mux.HandleFunc("GET /api/effects", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"categories": cat.Categories, "effects": cat.Effects})
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, buildState(ch, audio))
	})
	mux.HandleFunc("POST /api/chain", func(w http.ResponseWriter, r *http.Request) {
		var rq chainRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&rq); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := applyChain(ch, cat, rq); err != nil {
			httpError(w, http.StatusConflict, err)
			return
		}
		redraw()
		writeJSON(w, buildState(ch, audio))
	})
	mux.HandleFunc("POST /api/param", func(w http.ResponseWriter, r *http.Request) {
		var rq paramRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&rq); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if rq.Pos < 0 || rq.Pos >= ch.Len() || rq.Index < 0 || rq.Index >= ch.ParamCount(rq.Pos) {
			httpError(w, http.StatusConflict, errNoSlot)
			return
		}
		if rq.Reset {
			ch.ResetParam(rq.Pos, rq.Index)
		} else {
			ch.SetParam(rq.Pos, rq.Index, rq.Value)
		}
		redraw()
		writeJSON(w, map[string]any{"version": ch.Version(), "params": ch.Params(rq.Pos)})
	})
	mux.HandleFunc("POST /api/gains", func(w http.ResponseWriter, r *http.Request) {
		var rq struct {
			In  float64 `json:"in_gain"`
			Out float64 `json:"out_gain"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&rq); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ch.SetGains(rq.In, rq.Out)
		redraw()
		writeJSON(w, buildState(ch, audio))
	})
	return mux
}

func runWebServer(port int, ch *Chain, cat *Catalog, audio *audioStatus, surf *surface, stop <-chan struct{}) {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           newWebMux(ch, cat, audio, surf),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-stop
		_ = srv.Close()
	}()
	log.Printf("web page on :%d", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("web server: %v", err)
	}
}
