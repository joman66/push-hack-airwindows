package main

// port.go - which port the web page listens on.
//
// push-catalog assigns each hack a port when it installs it and writes it
// into the installed hack.json. This hack's own hack.json declares none.
// When the installed copy has no port anyway (a push-catalog that predates
// port assignment, a hand copy, a local run) or another hack's hack.json
// claims the same port, the hack picks the lowest port from 7711 up that no
// other hack claims and that is free right now, and writes it into its
// hack.json, so Push Manager links to the right place. It never falls back
// to a fixed port: a fixed default once collided with Audio Loopback.

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
)

const (
	firstHackPort = 7711 // 7701-7710 belong to the framework
	lastHackPort  = 7799
)

// claimedPorts reads the port of every other hack installed next to this one.
func claimedPorts(hackDir string) map[int]string {
	out := map[int]string{}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(hackDir), "*", "hack.json"))
	for _, hj := range matches {
		if filepath.Dir(hj) == hackDir {
			continue
		}
		b, err := os.ReadFile(hj)
		if err != nil {
			continue
		}
		var c struct {
			ID   string `json:"id"`
			Port int    `json:"port"`
		}
		if json.Unmarshal(b, &c) == nil && c.Port > 0 {
			out[c.Port] = filepath.Base(filepath.Dir(hj))
		}
	}
	return out
}

func portFree(p int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// pickPort is the lowest port in range that no other hack claims and that
// free() accepts.
func pickPort(claimed map[int]string, free func(int) bool) (int, error) {
	for p := firstHackPort; p <= lastHackPort; p++ {
		if _, taken := claimed[p]; !taken && free(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", firstHackPort, lastHackPort)
}

// writePort sets "port" in hack.json and keeps every other field.
func writePort(path string, port int) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	m["port"] = port
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), st.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// resolveWebPort returns the port to listen on, fixing hack.json if needed.
func resolveWebPort(configPath string) int {
	hackDir, _ := filepath.Abs(filepath.Dir(configPath))
	claimed := claimedPorts(hackDir)

	var cfg struct {
		Port int `json:"port"`
	}
	b, readErr := os.ReadFile(configPath)
	if readErr == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			log.Printf("%s: %v", configPath, err)
		}
	}
	if cfg.Port > 0 {
		owner, clash := claimed[cfg.Port]
		if !clash {
			return cfg.Port
		}
		log.Printf("WARNING: port %d in %s is also claimed by %s: picking another one", cfg.Port, configPath, owner)
	}

	port, err := pickPort(claimed, portFree)
	if err != nil {
		log.Printf("WARNING: %v: web page off", err)
		return 0
	}
	if readErr != nil {
		log.Printf("%s: %v (web page on %d, not saved)", configPath, readErr, port)
		return port
	}
	if err := writePort(configPath, port); err != nil {
		log.Printf("WARNING: web page on %d, but could not save it in %s: %v", port, configPath, err)
	} else {
		log.Printf("web page port %d saved in %s", port, configPath)
	}
	return port
}
