package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// hacksDir makes /data/push-hack/hacks/<id>/hack.json for each entry.
func hacksDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for id, body := range files {
		os.MkdirAll(filepath.Join(root, id), 0o755)
		os.WriteFile(filepath.Join(root, id, "hack.json"), []byte(body), 0o644)
	}
	return root
}

func readPort(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	p, _ := m["port"].(float64)
	return int(p), m
}

func TestPickPortSkipsClaimedAndBusy(t *testing.T) {
	claimed := map[int]string{7711: "push-braids", 7712: "push-audio-loopback"}
	busy := map[int]bool{7713: true}
	p, err := pickPort(claimed, func(p int) bool { return !busy[p] })
	if err != nil || p != 7714 {
		t.Fatalf("got %d %v, want 7714", p, err)
	}
}

// The reported bug: Audio Loopback has 7712, Airwindows' hack.json has no
// port. It must not end up on 7712, and must save what it picked.
func TestNoPortNextToLoopback(t *testing.T) {
	root := hacksDir(t, map[string]string{
		"push-audio-loopback": `{"id":"push-audio-loopback","port":7712}`,
		"airwindows":          `{"id":"airwindows","name":"Airwindows","web_ui":{"label":"Airwindows","path":"/"}}`,
	})
	cfg := filepath.Join(root, "airwindows", "hack.json")
	got := resolveWebPort(cfg)
	if got == 7712 || got < firstHackPort {
		t.Fatalf("picked %d", got)
	}
	saved, m := readPort(t, cfg)
	if saved != got {
		t.Fatalf("saved %d, listening on %d", saved, got)
	}
	if m["name"] != "Airwindows" || m["web_ui"] == nil {
		t.Fatalf("other fields lost: %v", m)
	}
	// next start: keeps it
	if again := resolveWebPort(cfg); again != got {
		t.Fatalf("second start moved to %d", again)
	}
}

// An installed copy that says 7712 while Audio Loopback also says 7712.
func TestClashingPortIsReplaced(t *testing.T) {
	root := hacksDir(t, map[string]string{
		"push-audio-loopback": `{"id":"push-audio-loopback","port":7712}`,
		"airwindows":          `{"id":"airwindows","port":7712}`,
	})
	cfg := filepath.Join(root, "airwindows", "hack.json")
	if got := resolveWebPort(cfg); got == 7712 {
		t.Fatal("kept a port another hack claims")
	}
	if saved, _ := readPort(t, cfg); saved == 7712 {
		t.Fatal("hack.json still says 7712")
	}
}

// A port the catalog assigned is used as it is.
func TestAssignedPortIsKept(t *testing.T) {
	root := hacksDir(t, map[string]string{
		"push-audio-loopback": `{"id":"push-audio-loopback","port":7712}`,
		"airwindows":          `{"id":"airwindows","port":7716}`,
	})
	if got := resolveWebPort(filepath.Join(root, "airwindows", "hack.json")); got != 7716 {
		t.Fatalf("got %d, want 7716", got)
	}
}
