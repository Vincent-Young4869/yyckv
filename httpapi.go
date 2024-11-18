package main

import (
	"fmt"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"strconv"
)

// Handler for a http based key-value store backed by raft
type httpKVAPI struct {
	// TODO: include the kv storage, raft node config, etc.
}

func (h *httpKVAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	router := mux.NewRouter()
	router.HandleFunc("/data/{key}", h.handleData).Methods(http.MethodPut, http.MethodGet, http.MethodDelete)
	router.HandleFunc("/node", h.handleNode).Methods(http.MethodPost, http.MethodDelete)
	router.ServeHTTP(w, r)
}

func (h *httpKVAPI) handleData(w http.ResponseWriter, r *http.Request) {
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

func (h *httpKVAPI) handleNode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		fmt.Println("new node adding")
		// ... POST logic
	case http.MethodDelete:
		fmt.Println("node removing")
		// ... DELETE logic
	}
}

// serveHTTPKVAPI starts a key-value server with a GET/PUT API and listens.
func serveHTTPKVAPI(port int, errorC <-chan error) {
	srv := http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: &httpKVAPI{},
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
