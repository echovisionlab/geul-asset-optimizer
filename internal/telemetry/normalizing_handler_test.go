package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestSharedNormalizingHandlerRedactsAndNormalizes(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	handler := NewNormalizingHandler(slog.NewJSONHandler(&output, nil))
	record := slog.NewRecord(time.Now(), slog.LevelError, "optimization failed", 0)
	record.AddAttrs(
		slog.String("jobId", "job-1"),
		slog.String("sourceKey", "private/key"),
		slog.Any("error", errors.New("private detail")),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["job_id"] != "job-1" || entry["error_type"] != "error_string" {
		t.Fatalf("normalized entry = %#v", entry)
	}
	if _, ok := entry["source_key"]; ok {
		t.Fatalf("sensitive source key was retained: %#v", entry)
	}
}
