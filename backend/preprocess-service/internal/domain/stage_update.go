package domain

import (
	"fmt"
	"time"
)

type StageUpdate struct {
	Stage          ProcessingStage
	StageStartedAt *time.Time
	StageUpdatedAt time.Time
}

func (u StageUpdate) Validate() error {
	if !u.Stage.IsValid() {
		return fmt.Errorf("processing stage is required")
	}
	if u.StageUpdatedAt.IsZero() {
		return fmt.Errorf("stage update time is required")
	}
	return nil
}
