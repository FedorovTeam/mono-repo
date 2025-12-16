package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNotifyCallbackSuccess(t *testing.T) {
	t.Helper()

	attempts := 0
	var received callbackPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++

		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("unexpected content type: %s", ct)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		defer r.Body.Close()

		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := &Worker{
		cfg:    Config{Backoff: 5 * time.Millisecond},
		http:   server.Client(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	job := QueueJob{JobID: "job-1", TargetBucket: "bucket", TargetKey: "result.docx", CallbackURL: server.URL}

	w.notifyCallback(context.Background(), job, statusDone, callbackErrorInfo{})

	if attempts != 1 {
		t.Fatalf("expected single attempt, got %d", attempts)
	}

	if received.JobID != job.JobID || received.Status != statusDone {
		t.Fatalf("unexpected payload job/status: %#v", received)
	}

	if received.TargetBucket != job.TargetBucket || received.TargetKey != job.TargetKey {
		t.Fatalf("unexpected target: %s/%s", received.TargetBucket, received.TargetKey)
	}

	if received.Error != nil {
		t.Fatalf("expected no error payload, got %+v", received.Error)
	}
}

func TestNotifyCallbackRetry(t *testing.T) {
	t.Helper()

	attempts := 0
	payloads := make([]callbackPayload, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var payload callbackPayload
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}

		payloads = append(payloads, payload)
		attempts++

		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := &Worker{
		cfg:    Config{Backoff: 5 * time.Millisecond},
		http:   server.Client(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	job := QueueJob{JobID: "job-2", TargetBucket: "bucket", TargetKey: "out.docx", CallbackURL: server.URL}
	errInfo := callbackErrorInfo{Code: "convert_failed", Message: "conversion failed"}

	w.notifyCallback(context.Background(), job, statusStopped, errInfo)

	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected two payloads, got %d", len(payloads))
	}

	if payloads[1].Error == nil || payloads[1].Error.Code != errInfo.Code || payloads[1].Error.Message != errInfo.Message {
		t.Fatalf("unexpected error payload: %+v", payloads[1].Error)
	}
}
