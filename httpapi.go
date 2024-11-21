package main

import (
	"fmt"
	"github.com/gorilla/mux"
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
	router.HandleFunc("/node/{id}", h.handleNodeRequest).Methods(http.MethodPost, http.MethodDelete)
	router.ServeHTTP(w, r)
}

func (h *httpKVAPI) handleDataRequest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	switch r.Method {
	case http.MethodPut:
		fmt.Printf("PUT key=%s\n", key)
		// ... PUT logic
	case http.MethodGet:
		fmt.Printf("GET key=%s\n", key)
		// ... GET logic
	case http.MethodDelete:
		fmt.Printf("DELETE key=%s\n", key)
		// ... DELETE logic
	}
}

func (h *httpKVAPI) handleNodeRequest(w http.ResponseWriter, r *http.Request) {
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
