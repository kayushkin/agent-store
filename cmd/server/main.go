package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	agentstore "github.com/kayushkin/agent-store"
)

func main() {
	addr := os.Getenv("AGENT_STORE_ADDR")
	if addr == "" {
		addr = ":8300"
	}
	dbPath := os.Getenv("AGENT_STORE_DB")
	if dbPath == "" {
		dbPath = agentstore.DefaultPath()
	}

	store, err := agentstore.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	mux := http.NewServeMux()
	agentstore.RegisterHandlers(mux, store)

	scanInterval := parseScanInterval()
	if scanInterval > 0 {
		go runAutoScanner(store, scanInterval)
	}

	log.Printf("agent-store listening on %s (db: %s, auto-scan=%s)", addr, dbPath, scanInterval)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// parseScanInterval reads AGENT_STORE_SCAN_INTERVAL_SECS. 0 disables.
// Default = 900 (15 minutes).
func parseScanInterval() time.Duration {
	raw := os.Getenv("AGENT_STORE_SCAN_INTERVAL_SECS")
	if raw == "" {
		return 15 * time.Minute
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		log.Printf("auto-scan: invalid AGENT_STORE_SCAN_INTERVAL_SECS=%q, using default 15m", raw)
		return 15 * time.Minute
	}
	return time.Duration(n) * time.Second
}

// runAutoScanner periodically re-walks $HOME so out-of-band edits to tracked
// files (someone touching ~/CLAUDE.md from a terminal, a git pull on a repo
// with AGENTS.md, etc.) are picked up without the user clicking Scan in dash.
// Each scan that detects a hash change appends a scan-import version row, so
// nothing is silently lost between scans.
func runAutoScanner(store *agentstore.Store, interval time.Duration) {
	// Stagger the first run so a fresh start doesn't compete with handler
	// traffic. After that, fire on the interval.
	time.Sleep(30 * time.Second)
	for {
		res, err := store.Scan()
		if err != nil {
			log.Printf("auto-scan: %v", err)
		} else if res.Added > 0 || res.Updated > 0 || res.Missing > 0 {
			log.Printf("auto-scan: %d scanned (%d new, %d updated, %d missing)",
				res.Scanned, res.Added, res.Updated, res.Missing)
		}
		time.Sleep(interval)
	}
}
