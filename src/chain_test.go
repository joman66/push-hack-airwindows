package main

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

var testCat *Catalog

func catalog(t testing.TB) *Catalog {
	t.Helper()
	if testCat == nil {
		testCat = newCatalog(loadCatalog())
	}
	return testCat
}

func newTestChain(t testing.TB, dir string) (*Chain, *cEngine) {
	t.Helper()
	eng := newCEngine(dspRate)
	t.Cleanup(eng.Free)
	return newChain(eng, catalog(t), dir), eng
}

func mustEffect(t testing.TB, name string) Effect {
	t.Helper()
	e, ok := catalog(t).ByName(name)
	if !ok {
		t.Fatalf("no effect %q", name)
	}
	return e
}

func sine(buf []float32, phase *float64, hz, amp float64) {
	for i := 0; i < len(buf)/2; i++ {
		v := float32(amp * math.Sin(*phase))
		buf[2*i], buf[2*i+1] = v, v
		*phase += 2 * math.Pi * hz / dspRate
	}
}

func TestCatalog(t *testing.T) {
	c := catalog(t)
	if len(c.Effects) < 400 {
		t.Fatalf("only %d effects", len(c.Effects))
	}
	if !sort.StringsAreSorted(c.Categories) {
		t.Fatal("categories not sorted")
	}
	for _, cat := range c.Categories {
		if cat == "Unclassified" {
			t.Fatal("Unclassified should be filtered out")
		}
		items := c.InCategory(cat)
		if len(items) == 0 {
			t.Fatalf("empty category %s", cat)
		}
		for i := 1; i < len(items); i++ {
			if items[i-1].Name > items[i].Name {
				t.Fatalf("%s not sorted by name: %s > %s", cat, items[i-1].Name, items[i].Name)
			}
		}
	}
	for _, e := range c.Effects {
		for _, r := range e.Name + e.Category {
			if r > 126 || r < 32 {
				t.Fatalf("non-ASCII in %q / %q: the panel cannot draw it", e.Name, e.Category)
			}
		}
	}
	t.Logf("%d effects, %d categories", len(c.Effects), len(c.Categories))
}

// TestEveryEffectProcesses loads every effect alone, runs a second of a
// sine through it at its default settings, and checks the output is finite.
// It also reports the slowest effects, against the 2902 us block budget.
func TestEveryEffectProcesses(t *testing.T) {
	ch, eng := newTestChain(t, "")
	buf := make([]float32, 2*defaultPeriod)
	type timing struct {
		name string
		us   float64
	}
	var times []timing
	for _, e := range catalog(t).Effects {
		if _, err := ch.Insert(0, e.Index); err != nil {
			t.Fatalf("%s: insert: %v", e.Name, err)
		}
		if got := ch.ParamCount(0); got != e.NParams {
			t.Errorf("%s: %d params, catalog says %d", e.Name, got, e.NParams)
		}
		for p, pr := range ch.Params(0) {
			if pr.Name == "" {
				t.Errorf("%s: param %d has no name", e.Name, p)
			}
		}
		phase := 0.0
		eng.Stats()
		start := time.Now()
		blocks := dspRate / defaultPeriod
		for b := 0; b < blocks; b++ {
			sine(buf, &phase, 220, 0.5)
			eng.Process(buf)
		}
		per := float64(time.Since(start).Microseconds()) / float64(blocks)
		times = append(times, timing{e.Name, per})
		if s := eng.Stats(); s.Bad > 0 {
			t.Errorf("%s: %d non-finite samples", e.Name, s.Bad)
		}
		if err := ch.Remove(0); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(times, func(a, b int) bool { return times[a].us > times[b].us })
	total := 0.0
	for _, x := range times {
		total += x.us
	}
	t.Logf("mean %.1f us per 128-frame block; slowest:", total/float64(len(times)))
	for _, x := range times[:8] {
		t.Logf("  %-22s %7.1f us (%.1f%% of a block)", x.name, x.us, x.us/2902*100)
	}
}

func TestChainOps(t *testing.T) {
	ch, eng := newTestChain(t, "")
	names := []string{"Galactic", "Pressure5", "ToTape9", "Air4"}
	for _, n := range names {
		if _, err := ch.Insert(-1, mustEffect(t, n).Index); err != nil {
			t.Fatal(err)
		}
	}
	order := func() []string {
		var out []string
		for _, s := range ch.Slots() {
			out = append(out, s.Name)
		}
		return out
	}
	want := func(w ...string) {
		t.Helper()
		got := order()
		if len(got) != len(w) {
			t.Fatalf("order %v, want %v", got, w)
		}
		for i := range w {
			if got[i] != w[i] {
				t.Fatalf("order %v, want %v", got, w)
			}
		}
	}
	want("Galactic", "Pressure5", "ToTape9", "Air4")

	if err := ch.Move(0, 2); err != nil {
		t.Fatal(err)
	}
	want("Pressure5", "ToTape9", "Galactic", "Air4")
	if err := ch.Move(3, 0); err != nil {
		t.Fatal(err)
	}
	want("Air4", "Pressure5", "ToTape9", "Galactic")

	// a parameter change follows its slot through a move
	ch.SetParam(3, 0, 0.123)
	if err := ch.Move(3, 1); err != nil {
		t.Fatal(err)
	}
	if v := ch.Params(1)[0].Value; math.Abs(v-0.123) > 1e-6 {
		t.Fatalf("param after move = %v", v)
	}
	ch.ResetParam(1, 0)
	if p := ch.Params(1)[0]; p.Value != p.Default {
		t.Fatalf("reset: %v != default %v", p.Value, p.Default)
	}

	ch.SetBypass(0, true)
	if err := ch.Replace(0, mustEffect(t, "Mackity").Index); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := ch.Slot(0); s.Name != "Mackity" || !s.Bypass {
		t.Fatalf("replace: %+v", s)
	}
	if err := ch.Remove(2); err != nil {
		t.Fatal(err)
	}
	want("Mackity", "Galactic", "ToTape9")

	for ch.Len() < MaxSlots {
		if _, err := ch.Insert(-1, mustEffect(t, "PurestGain").Index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ch.Insert(-1, mustEffect(t, "PurestGain").Index); err != errFull {
		t.Fatalf("9th insert: %v, want errFull", err)
	}
	if ch.Remove(99) == nil || ch.Move(0, 99) == nil || ch.Replace(-1, 0) == nil {
		t.Fatal("out of range accepted")
	}
	if eng.Len() != MaxSlots {
		t.Fatalf("engine len %d", eng.Len())
	}
}

func TestBypassIsTransparent(t *testing.T) {
	ch, eng := newTestChain(t, "")
	ch.Insert(-1, mustEffect(t, "Density").Index)
	ch.SetParam(0, 0, 1) // heavy saturation
	ch.SetBypass(0, true)
	buf := make([]float32, 2*defaultPeriod)
	ref := make([]float32, len(buf))
	phase := 0.0
	sine(buf, &phase, 440, 0.8)
	copy(ref, buf)
	eng.Process(buf)
	for i := range buf {
		if buf[i] != ref[i] {
			t.Fatalf("sample %d changed while bypassed: %v -> %v", i, ref[i], buf[i])
		}
	}
	// and gains apply
	ch.SetGains(0.5, 1)
	copy(buf, ref)
	eng.Process(buf)
	if math.Abs(float64(buf[10]-ref[10]*0.5)) > 1e-6 {
		t.Fatalf("in gain not applied: %v vs %v", buf[10], ref[10]*0.5)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	ch, _ := newTestChain(t, dir)
	ch.Insert(-1, mustEffect(t, "Galactic").Index)
	ch.Insert(-1, mustEffect(t, "ToTape9").Index)
	ch.SetParam(0, 1, 0.77)
	ch.SetBypass(1, true)
	ch.SetGains(0.8, 1.25)
	if err := ch.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatal(err)
	}

	ch2, _ := newTestChain(t, dir)
	if err := ch2.Load(); err != nil {
		t.Fatal(err)
	}
	s := ch2.Slots()
	if len(s) != 2 || s[0].Name != "Galactic" || s[1].Name != "ToTape9" || !s[1].Bypass || s[0].Bypass {
		t.Fatalf("loaded %+v", s)
	}
	if math.Abs(s[0].Params[1]-0.77) > 1e-6 {
		t.Fatalf("param %v", s[0].Params[1])
	}
	if in, out := ch2.Gains(); in != 0.8 || out != 1.25 {
		t.Fatalf("gains %v %v", in, out)
	}

	// an effect that no longer exists is skipped, the rest loads
	os.WriteFile(filepath.Join(dir, stateFileName), []byte(`{"slots":[{"name":"NoSuchThing"},{"name":"Air4","params":[0.5]}]}`), 0o644)
	ch3, _ := newTestChain(t, dir)
	if err := ch3.Load(); err != nil {
		t.Fatal(err)
	}
	if s := ch3.Slots(); len(s) != 1 || s[0].Name != "Air4" {
		t.Fatalf("loaded %+v", s)
	}
	if in, out := ch3.Gains(); in != 1 || out != 1 {
		t.Fatalf("missing gains should be unity, got %v %v", in, out)
	}
}

// TestConcurrentEdits runs the audio loop while another goroutine edits the
// chain as fast as it can, as Push and the web page would. Run with -race.
func TestConcurrentEdits(t *testing.T) {
	ch, eng := newTestChain(t, "")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		buf := make([]float32, 2*defaultPeriod)
		phase := 0.0
		for {
			select {
			case <-stop:
				close(done)
				return
			default:
			}
			sine(buf, &phase, 330, 0.5)
			eng.Process(buf)
		}
	}()
	effs := catalog(t).Effects
	for i := 0; i < 400; i++ {
		e := effs[(i*37)%len(effs)]
		switch i % 5 {
		case 0, 1:
			ch.Insert(i%3, e.Index)
		case 2:
			ch.Move(0, ch.Len()-1)
		case 3:
			ch.Replace(0, e.Index)
		case 4:
			ch.Remove(0)
		}
		ch.NudgeParam(0, 0, 0.01)
		ch.ToggleBypass(1)
		_ = ch.Params(0)
	}
	close(stop)
	<-done
	if s := eng.Stats(); s.Bad > 0 {
		t.Fatalf("%d non-finite samples", s.Bad)
	}
}
