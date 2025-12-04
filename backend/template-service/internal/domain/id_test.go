package domain

import "testing"

func TestGetRegionID_Default(t *testing.T) {
	SetRegionID("")
	if GetRegionID() != "default-region" {
		t.Fatalf("expected default-region, got %s", GetRegionID())
	}
}

func TestGetRegionID_Set(t *testing.T) {
	SetRegionID("test")
	if GetRegionID() != "test" {
		t.Fatalf("expected test, got %s", GetRegionID())
	}
}
