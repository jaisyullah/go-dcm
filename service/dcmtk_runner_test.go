package service

import (
	"context"
	"testing"
	"time"
)

// TestRunDCMTK_InvalidTool tests that a non-existent tool returns an error.
func TestRunDCMTK_InvalidTool(t *testing.T) {
	ctx := context.Background()
	err := RunDCMTK(ctx, "nonexistent_tool_xyz", "/tmp/in", "/tmp/out", nil)
	if err == nil {
		t.Fatal("expected error for non-existent tool")
	}
}

// TestCheckToolAvailable_ExistingTool tests checking an existing tool.
func TestCheckToolAvailable_ExistingTool(t *testing.T) {
	err := CheckToolAvailable("img2dcm")
	if err != nil {
		t.Skipf("img2dcm not available: %v", err)
	}
}

// TestCheckToolAvailable_MissingTool tests checking a missing tool.
func TestCheckToolAvailable_MissingTool(t *testing.T) {
	err := CheckToolAvailable("totally_fake_tool_12345")
	if err == nil {
		t.Fatal("expected error for non-existent tool")
	}
}

// TestRunDCMTK_Timeout tests command timeout propagation.
func TestRunDCMTK_Timeout(t *testing.T) {
	// This test uses a very short context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// We use a real tool but with a timeout that is guaranteed to expire
	err := RunDCMTK(ctx, "img2dcm", "/nonexistent", "/tmp/out", nil)
	if err == nil {
		t.Fatal("expected error due to timeout or execution failure")
	}
}
