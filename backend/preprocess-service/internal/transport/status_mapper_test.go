package transport

import (
	"testing"

	"preprocess-service/internal/domain"
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

func TestToProtoStatus(t *testing.T) {
	tests := []struct {
		name   string
		status domain.PreprocessStatus
		expect preprocessv1.PreprocessStatus
	}{
		{name: "pending", status: domain.StatusPending, expect: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING},
		{name: "processing", status: domain.StatusProcessing, expect: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PROCESSING},
		{name: "completed", status: domain.StatusCompleted, expect: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED},
		{name: "failed", status: domain.StatusFailed, expect: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED},
		{name: "stopped", status: domain.StatusStopped, expect: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_STOPPED},
		{name: "unspecified", status: domain.StatusUnspecified, expect: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toProtoStatus(tt.status); got != tt.expect {
				t.Fatalf("expected %v, got %v", tt.expect, got)
			}
		})
	}
}

func TestStatusFromProto(t *testing.T) {
	tests := []struct {
		name   string
		status preprocessv1.PreprocessStatus
		expect domain.PreprocessStatus
	}{
		{name: "pending", status: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING, expect: domain.StatusPending},
		{name: "processing", status: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PROCESSING, expect: domain.StatusProcessing},
		{name: "completed", status: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED, expect: domain.StatusCompleted},
		{name: "failed", status: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED, expect: domain.StatusFailed},
		{name: "stopped", status: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_STOPPED, expect: domain.StatusStopped},
		{name: "unspecified", status: preprocessv1.PreprocessStatus_PREPROCESS_STATUS_UNSPECIFIED, expect: domain.StatusUnspecified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusFromProto(tt.status); got != tt.expect {
				t.Fatalf("expected %s, got %s", tt.expect, got)
			}
		})
	}
}
