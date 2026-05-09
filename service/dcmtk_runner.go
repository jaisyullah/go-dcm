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

// RunDCMTK executes a DCMTK command line utility with timeout and output capture.
//
// tool: name of the executable (e.g. "img2dcm", "pdf2dcm", "cda2dcm", "stl2dcm")
// inputFile: path to input file (e.g., uploaded jpg or pdf)
// outputFile: path to resulting dicom file
// extraArgs: additional arguments parsed from request
func RunDCMTK(tool string, inputFile string, outputFile string, extraArgs []string) error {
	return RunDCMTKWithTimeout(tool, inputFile, outputFile, extraArgs, DefaultTimeout)
}

// RunDCMTKWithTimeout executes a DCMTK command with a specified timeout.
func RunDCMTKWithTimeout(tool string, inputFile string, outputFile string, extraArgs []string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Construct the command: tool [options] inputFile outputFile
	var cmdArgs []string
	cmdArgs = append(cmdArgs, extraArgs...)
	cmdArgs = append(cmdArgs, inputFile, outputFile)

	cmd := exec.CommandContext(ctx, tool, cmdArgs...)

	// Capture output for structured logging and error reporting
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("executing DCMTK command",
		"tool", tool,
		"input", inputFile,
		"output", outputFile,
		"args", extraArgs,
	)

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			slog.Error("DCMTK command timed out",
				"tool", tool,
				"timeout", timeout.String(),
			)
			return fmt.Errorf("%s timed out after %s", tool, timeout)
		}

		slog.Error("DCMTK command failed",
			"tool", tool,
			"error", err.Error(),
			"stderr", stderr.String(),
			"stdout", stdout.String(),
		)
		return fmt.Errorf("failed to run %s: %w — stderr: %s", tool, err, stderr.String())
	}

	slog.Info("DCMTK command completed successfully",
		"tool", tool,
		"output", outputFile,
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
