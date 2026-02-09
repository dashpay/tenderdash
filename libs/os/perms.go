package os

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// PermissionError represents a detailed permission error with diagnostic information
type PermissionError struct {
	Path           string
	Operation      string
	OriginalError  error
	FileUID        int
	FileGID        int
	FileMode       os.FileMode
	ProcessUID     int
	ProcessGID     int
	ProcessEUID    int
	ProcessEGID    int
	ParentDirMode  os.FileMode
	ParentDirUID   int
	ParentDirGID   int
	IsPermissionIssue bool
}

// Error implements the error interface
func (e *PermissionError) Error() string {
	if !e.IsPermissionIssue {
		return fmt.Sprintf("%s %q: %v", e.Operation, e.Path, e.OriginalError)
	}

	msg := fmt.Sprintf("%s %q: permission denied\n", e.Operation, e.Path)
	msg += "Permission Diagnostics:\n"
	msg += fmt.Sprintf("  File/Directory: %s\n", e.Path)

	if e.FileMode != 0 {
		msg += fmt.Sprintf("  File Mode: %s\n", e.FileMode)
		msg += fmt.Sprintf("  File Owner: UID=%d GID=%d\n", e.FileUID, e.FileGID)
	} else {
		msg += "  File: does not exist yet (will be created)\n"
		msg += fmt.Sprintf("  Parent Directory: %s\n", filepath.Dir(e.Path))
		msg += fmt.Sprintf("  Parent Dir Mode: %s\n", e.ParentDirMode)
		msg += fmt.Sprintf("  Parent Dir Owner: UID=%d GID=%d\n", e.ParentDirUID, e.ParentDirGID)
	}

	msg += fmt.Sprintf("  Process UID: %d (Effective: %d)\n", e.ProcessUID, e.ProcessEUID)
	msg += fmt.Sprintf("  Process GID: %d (Effective: %d)\n", e.ProcessGID, e.ProcessEGID)
	msg += "\n"
	msg += "Workaround for Docker/container environments:\n"
	msg += "\n"

	// Determine the directory to fix
	targetDir := e.Path
	if e.FileMode == 0 {
		// File doesn't exist, use parent directory
		targetDir = filepath.Dir(e.Path)
	} else if !e.FileMode.IsDir() {
		// It's a file, use its parent directory
		targetDir = filepath.Dir(e.Path)
	}

	msg += "  Option 1: Fix ownership from host (if volume is mounted from host):\n"
	msg += fmt.Sprintf("    sudo chown -R %d:%d %s && sudo chmod -R 755 %s\n", e.ProcessUID, e.ProcessGID, targetDir, targetDir)
	msg += "\n"
	msg += "  Option 2: Fix from inside running container:\n"
	msg += fmt.Sprintf("    docker exec -it --user root <CONTAINER_NAME> sh -c 'chown -R %d:%d %s && chmod -R 755 %s'\n", e.ProcessUID, e.ProcessGID, targetDir, targetDir)

	return msg
}

// Unwrap returns the underlying error
func (e *PermissionError) Unwrap() error {
	return e.OriginalError
}

// CheckFileAccess checks if a file or directory is accessible and returns detailed
// permission information if there's an issue
func CheckFileAccess(path string, operation string) error {
	// Get current process credentials
	processUID := os.Getuid()
	processGID := os.Getgid()
	processEUID := os.Geteuid()
	processEGID := os.Getegid()

	// Try to stat the file
	fileInfo, err := os.Stat(path)

	permErr := &PermissionError{
		Path:        path,
		Operation:   operation,
		ProcessUID:  processUID,
		ProcessGID:  processGID,
		ProcessEUID: processEUID,
		ProcessEGID: processEGID,
	}

	if err != nil {
		permErr.OriginalError = err

		// Check if it's a permission error
		if isPermissionError(err) {
			permErr.IsPermissionIssue = true

			// File doesn't exist, check parent directory
			parentDir := filepath.Dir(path)
			if parentInfo, parentErr := os.Stat(parentDir); parentErr == nil {
				permErr.ParentDirMode = parentInfo.Mode()
				if stat, ok := parentInfo.Sys().(*syscall.Stat_t); ok {
					permErr.ParentDirUID = int(stat.Uid)
					permErr.ParentDirGID = int(stat.Gid)
				}
			}

			return permErr
		}

		// Not a permission error, return as-is
		return err
	}

	// File exists, gather its information
	permErr.FileMode = fileInfo.Mode()

	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		permErr.FileUID = int(stat.Uid)
		permErr.FileGID = int(stat.Gid)
	}

	return nil
}

// CheckDirectoryWritable checks if a directory is writable by creating and removing a test file
func CheckDirectoryWritable(dir string) error {
	err := CheckFileAccess(dir, "access directory")
	if err != nil {
		return err
	}

	// Try to create a test file
	testFile := filepath.Join(dir, ".tenderdash_write_test")
	f, err := os.Create(testFile)
	if err != nil {
		permErr := &PermissionError{
			Path:        dir,
			Operation:   "write to directory",
			OriginalError: err,
			ProcessUID:  os.Getuid(),
			ProcessGID:  os.Getgid(),
			ProcessEUID: os.Geteuid(),
			ProcessEGID: os.Getegid(),
		}

		if isPermissionError(err) {
			permErr.IsPermissionIssue = true
			if dirInfo, statErr := os.Stat(dir); statErr == nil {
				permErr.FileMode = dirInfo.Mode()
				if stat, ok := dirInfo.Sys().(*syscall.Stat_t); ok {
					permErr.FileUID = int(stat.Uid)
					permErr.FileGID = int(stat.Gid)
				}
			}
			return permErr
		}

		return err
	}

	f.Close()
	os.Remove(testFile)
	return nil
}

// WrapPermissionError wraps an error with detailed permission diagnostics if it's a permission error
func WrapPermissionError(path string, operation string, err error) error {
	if err == nil {
		return nil
	}

	// If it's already a PermissionError, return it
	var permErr *PermissionError
	if errors.As(err, &permErr) {
		return err
	}

	// Check if it's a permission-related error
	if !isPermissionError(err) {
		return err
	}

	// Get detailed diagnostics
	diagErr := CheckFileAccess(path, operation)
	if diagErr != nil {
		// If CheckFileAccess returned a PermissionError, use it
		if errors.As(diagErr, &permErr) {
			permErr.OriginalError = err
			return permErr
		}
	}

	// Create a basic PermissionError
	return &PermissionError{
		Path:          path,
		Operation:     operation,
		OriginalError: err,
		ProcessUID:    os.Getuid(),
		ProcessGID:    os.Getgid(),
		ProcessEUID:   os.Geteuid(),
		ProcessEGID:   os.Getegid(),
		IsPermissionIssue: true,
	}
}

// isPermissionError checks if an error is related to permissions
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for permission denied errors
	if errors.Is(err, os.ErrPermission) {
		return true
	}

	// Check syscall errors
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EACCES || errno == syscall.EPERM
	}

	// Check error message as fallback
	errMsg := err.Error()
	return contains(errMsg, "permission denied") ||
	       contains(errMsg, "access denied") ||
	       contains(errMsg, "operation not permitted")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		 indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
