package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/echovisionlab/geul-asset-optimizer/internal/config"
)

type connectionHealth interface {
	Healthy() bool
}

func startHealthServer(deps dependencies, cfg *config.Config, mqConn connectionHealth) (*http.Server, <-chan error, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(mqConn))
	server := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: mux}
	listener, err := deps.listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen health endpoint: %w", err)
	}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("Starting HTTP server", "port", cfg.Port)
		if listenErr := deps.serve(server, listener); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("serve health endpoint: %w", listenErr)
		}
	}()
	return server, serverErrors, nil
}

func healthHandler(mqConn connectionHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		status := struct {
			Status     string `json:"status"`
			PostgreSQL bool   `json:"postgresql"`
		}{Status: "ok", PostgreSQL: mqConn.Healthy()}
		if !status.PostgreSQL {
			status.Status = "degraded"
		}

		w.Header().Set("Content-Type", "application/json")
		if status.Status == "ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(status)
	}
}
