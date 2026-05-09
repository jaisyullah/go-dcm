package service

import (
	"testing"
)

// TestRunDCMTK_InvalidTool tests that a non-existent tool returns an error.
func TestRunDCMTK_InvalidTool(t *testing.T) {
	err := RunDCMTK("nonexistent_tool_xyz", "/tmp/in", "/tmp/out", nil)
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

// TestRunDCMTKWithTimeout_Timeout tests command timeout.
func TestRunDCMTKWithTimeout_Timeout(t *testing.T) {
	// This test uses a very short timeout with a tool that would succeed normally
	// We just verify the timeout mechanism doesn't panic or deadlock
	err := RunDCMTKWithTimeout("img2dcm", "/nonexistent", "/tmp/out", nil, 1)
	if err == nil {
		t.Fatal("expected error (either timeout or execution failure)")
	}
}
