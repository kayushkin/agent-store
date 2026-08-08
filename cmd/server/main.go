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
	// This process owns its mux, so it serves its own /health. Hosts that
	// embed agent-store must not -- see RegisterHealthHandler.
	agentstore.RegisterHealthHandler(mux)

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
		} else if res.Added > 0 || res.Updated > 0 || res.Missing > 0 || len(res.Errors) > 0 {
			// This condition is a noise gate: a scan that found nothing must
			// say nothing, or the interesting runs are unfindable. It only
			// works because Updated counts content changes. While Updated
			// meant "the row already existed" the gate was true on every run
			// -- measured 2026-08-08, 2641 of this service's 2657 journal
			// lines were this message reporting zero real work, one every 15
			// minutes since 2026-07-11, with the handful of genuine 1-new and
			// 2-missing runs buried among them.
			//
			// Errors joined the gate because a run that dropped a file is
			// exactly an interesting run, and it is the only reader that never
			// sees the JSON the HTTP handler returns. Scanned is an undercount
			// whenever the last number is nonzero, so the line says so rather
			// than leaving a reader to notice the arithmetic.
			log.Printf("auto-scan: %d scanned (%d new, %d updated, %d unchanged, %d missing, %d failed)",
				res.Scanned, res.Added, res.Updated, res.Unchanged, res.Missing, len(res.Errors))
			// One line per failure: a count tells nobody which file went
			// unrecorded, and that is the question the scan was asked.
			for _, e := range res.Errors {
				log.Printf("auto-scan: unaccounted %s (%s): %s", e.Path, e.Stage, e.Err)
			}
		}
		time.Sleep(interval)
	}
}
