package handler

import "net/http"

type Health struct{}

func (h *Health) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
