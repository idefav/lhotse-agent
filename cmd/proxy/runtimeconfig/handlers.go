package runtimeconfig

import (
	"context"
	"encoding/json"
	"net/http"
)

func RegisterHandlers(mux *http.ServeMux, manager *Manager) {
	mux.HandleFunc("/credential-runtime", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manager.Status())
	})
	mux.HandleFunc("/credential-runtime/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := manager.Reload(context.Background()); err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manager.Status())
	})
}
