package main

import (
	"context"
	"os"
)

func waitForShutdown(ctx context.Context, sigChan <-chan os.Signal, serverErrors <-chan error) (string, os.Signal, error) {
	shutdownReason := "signal"
	var shutdownSignal os.Signal
	var runErr error
	select {
	case shutdownSignal = <-sigChan:
	case <-ctx.Done():
		shutdownReason = "context_cancelled"
	case runErr = <-serverErrors:
		shutdownReason = "http_server"
	}
	return shutdownReason, shutdownSignal, runErr
}
