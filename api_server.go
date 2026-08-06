package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
)

type apiRequest struct {
	URL    string `json:"url"`
	Output string `json:"output,omitempty"`
}

func postDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	var req apiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	t := EnqueueTask(req.URL, req.Output)
	resp := map[string]string{
		"id":         t.ID,
		"status_url": fmt.Sprintf("/status?id=%s", t.ID),
		"result_url": fmt.Sprintf("/result?id=%s", t.ID),
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

func getStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	t, ok := GetTask(id)
	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(t)
}

func getResultHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	t, ok := GetTask(id)
	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	switch t.Status {
	case "done":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(t)
	case "error":
		http.Error(w, t.Err, http.StatusInternalServerError)
	default:
		// pending or running
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": t.Status})
	}
}

func StartAPIServer(addr string) {
	// initialize workers from env or default
	workers := 2
	if v := os.Getenv("API_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}
	InitTaskQueue(workers)

	http.HandleFunc("/download", postDownloadHandler)
	http.HandleFunc("/status", getStatusHandler)
	http.HandleFunc("/result", getResultHandler)
	log.Printf("API server listening on %s (workers=%d)", addr, workers)
	// graceful shutdown not implemented here
	log.Fatal(http.ListenAndServe(addr, nil))
}
