package service

import (
	"fmt"
	"os"
	"os/exec"
)

// RunDCMTK executes a DCMTK command line utility
// tool: name of the executable (e.g. "img2dcm", "dcmencap")
// inputFile: path to input file (e.g., uploaded jpg or pdf)
// outputFile: path to resulting dicom file
// args: additional arguments parsed from request
func RunDCMTK(tool string, inputFile string, outputFile string, extraArgs []string) error {
	// Construct the command
	// For example: img2dcm.exe [options] inputFile outputFile
	var cmdArgs []string
	
	// Add extra arguments first
	cmdArgs = append(cmdArgs, extraArgs...)
	
	// Final arguments are input file and output file
	// Some tools like dcmencap (pdf2dcm) have same format: dcmencap [options] docfile-in dcmfile-out
	cmdArgs = append(cmdArgs, inputFile, outputFile)
	
	cmd := exec.Command(tool, cmdArgs...)
	
	// Capture output for debugging
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run %s: %w", tool, err)
	}
	
	return nil
}
