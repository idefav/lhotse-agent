package domainpolicy

import (
	"context"
	"net/http"
)

func RegisterHandlers(mux *http.ServeMux, manager *Manager) {
	mux.HandleFunc("/domain-policy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		WriteStatus(w, manager.Status())
	})
	mux.HandleFunc("/domain-policy/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := manager.Reload(context.Background()); err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		WriteStatus(w, manager.Status())
	})
}
