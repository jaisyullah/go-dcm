package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// DefaultTimeout is the default command execution timeout.
const DefaultTimeout = 60 * time.Second

// RunDCMTK executes a DCMTK command line utility with context propagation.
// Note: It now accepts the request/job context!
func RunDCMTK(ctx context.Context, tool string, inputFile string, outputFile string, extraArgs []string) error {
	// Use the provided context. If the job times out or is cancelled,
	// Go automatically sends SIGKILL to the OS process.
	var cmdArgs []string
	cmdArgs = append(cmdArgs, extraArgs...)
	cmdArgs = append(cmdArgs, inputFile, outputFile)

	cmd := exec.CommandContext(ctx, tool, cmdArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.InfoContext(ctx, "executing DCMTK command",
		"tool", tool,
		"input", inputFile,
	)

	if err := cmd.Run(); err != nil {
		// Check if cancellation caused the error
		if ctx.Err() != nil {
			slog.ErrorContext(ctx, "DCMTK command cancelled or timed out",
				"tool", tool,
				"reason", ctx.Err().Error(),
			)
			return fmt.Errorf("%s execution cancelled: %w", tool, ctx.Err())
		}

		slog.ErrorContext(ctx, "DCMTK command failed",
			"tool", tool,
			"error", err.Error(),
			"stderr", stderr.String(),
		)
		return fmt.Errorf("failed to run %s: %w — stderr: %s", tool, err, stderr.String())
	}

	slog.InfoContext(ctx, "DCMTK command completed successfully",
		"tool", tool,
	)

	return nil
}

// CheckToolAvailable verifies a DCMTK tool is accessible on the system PATH.
func CheckToolAvailable(tool string) error {
	_, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s not found in PATH: %w", tool, err)
	}
	return nil
}
