package callback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"preprocess-service/internal/usecase/ports"
)

func TestClientLogsSend(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	core, logs := observer.New(zap.InfoLevel)
	client := NewClient(server.Client(), zap.New(core))

	payload := ports.CallbackPayload{
		JobID:  "job",
		Status: "done",
	}

	if _, err := client.Send(context.Background(), server.URL, payload, 2); err != nil {
		t.Fatalf("send returned error: %v", err)
	}

	if logs.Len() == 0 {
		t.Fatalf("expected logger to be called during send")
	}

	foundAttempt := false
	for _, log := range logs.All() {
		if attempt, ok := log.ContextMap()["attempt"]; ok && attempt == int64(2) {
			foundAttempt = true
			break
		}
	}

	if !foundAttempt {
		t.Fatalf("expected attempt to be logged")
	}
}
