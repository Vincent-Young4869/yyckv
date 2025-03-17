package main

import (
	"fmt"
	"github.com/gorilla/mux"
	"io"
	"log"
	"net/http"
	"strconv"

	"yyckv/kv"
)

// Handler for a http based key-value store backed by raft
type httpKVAPI struct {
	// TODO: include the kv storage, raft node config, etc.
	kv *kv.Kvstore
}

func (h *httpKVAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	router := mux.NewRouter()
	router.HandleFunc("/data/{key}", h.handleDataRequest).Methods(http.MethodPut, http.MethodGet, http.MethodDelete)
	router.HandleFunc("/node/{id}", h.handleTopologyChangeRequest).Methods(http.MethodPost, http.MethodDelete)
	router.ServeHTTP(w, r)
}

func (h *httpKVAPI) handleDataRequest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]
	defer r.Body.Close()
	switch r.Method {
	case http.MethodPut:
		v, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Failed to read on PUT (%v)\n", err)
			http.Error(w, "Failed on PUT", http.StatusBadRequest)
			return
		}

		h.kv.Propose(key, string(v))

		// Optimistic-- no waiting for ack from raft. Value is not yet
		// committed so a subsequent GET on the key may return old value
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		if v, ok := h.kv.Lookup(key); ok {
			w.Write([]byte(v))
		} else {
			http.Error(w, "Failed to GET", http.StatusNotFound)
		}
	case http.MethodDelete:
		fmt.Printf("DELETE key=%s\n", key)
		// ... DELETE logic
	}
}

func (h *httpKVAPI) handleTopologyChangeRequest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid node ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		fmt.Printf("new node %d adding", id)
		// ... POST logic
	case http.MethodDelete:
		fmt.Printf("node %d removing", id)
		// ... DELETE logic
	}
}

// serveHTTPKVAPI starts a key-value server with a GET/PUT API and listens.
func serveHTTPKVAPI(kvStore *kv.Kvstore, port int, errorC <-chan error) {
	srv := http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: &httpKVAPI{
			kv: kvStore,
		},
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	// exit when raft goes down
	if err, ok := <-errorC; ok {
		log.Fatal(err)
	}
}
