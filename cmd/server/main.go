package main

import (
	"log"
	"net/http"
	"os"

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

	log.Printf("agent-store server listening on %s (db: %s)", addr, dbPath)
	log.Fatal(http.ListenAndServe(addr, mux))
}
