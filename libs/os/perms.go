package os

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Operation constants for file access checks
const (
	OperationRead           = "read"
	OperationWrite          = "write"
	OperationExecute        = "execute"
	OperationReadFile       = "read file"
	OperationWriteFile      = "write file"
	OperationReadDirectory  = "read directory"
	OperationWriteDirectory = "write directory"
	OperationOpenDatabase   = "open database"
)

// PermissionError represents a detailed permission error with diagnostic information
type PermissionError struct {
	Path              string
	Operation         string
	OriginalError     error
	FileUID           int
	FileGID           int
	FileMode          os.FileMode
	ProcessUID        int
	ProcessGID        int
	ProcessEUID       int
	ProcessEGID       int
	ParentDirMode     os.FileMode
	ParentDirUID      int
	ParentDirGID      int
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

	cmd := fmt.Sprintf(
		"chown -R %d:%d %s && find %s -type d -exec chmod 750 \\{\\} \\; && find %s -type f -exec chmod 640 \\{\\} \\;",
		e.ProcessUID, e.ProcessGID, targetDir, targetDir, targetDir)

	msg += "  Option 1: Fix ownership from host (if volume is mounted from host):\n"
	msg += fmt.Sprintf("    sudo bash -c '%s'\n", cmd)

	msg += "\n"
	msg += "  Option 2: Fix from inside running container:\n"
	msg += fmt.Sprintf("    docker exec -it --user root <CONTAINER_NAME> sh -c '%s'\n",
		cmd)

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

			// Can't stat the target; collect parent directory info for diagnostics
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

	// Map operation to access flags and verify actual access
	accessFlags, err := mapOperationToAccessFlags(operation)
	if err != nil {
		return fmt.Errorf("check file access for %q: %w", path, err)
	}
	if err := unix.Access(path, accessFlags); err != nil {
		permErr.OriginalError = err
		permErr.IsPermissionIssue = true
		return permErr
	}

	return nil
}

// mapOperationToAccessFlags maps operation constants to unix access flags
func mapOperationToAccessFlags(operation string) (uint32, error) {
	switch operation {
	case OperationWrite, OperationWriteFile, OperationWriteDirectory, OperationOpenDatabase:
		return unix.W_OK, nil
	case OperationExecute:
		return unix.X_OK, nil
	case OperationRead, OperationReadFile, OperationReadDirectory:
		return unix.R_OK, nil
	}

	return 0, fmt.Errorf("unsupported operation %q", operation)
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
		Path:              path,
		Operation:         operation,
		OriginalError:     err,
		ProcessUID:        os.Getuid(),
		ProcessGID:        os.Getgid(),
		ProcessEUID:       os.Geteuid(),
		ProcessEGID:       os.Getegid(),
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
	return strings.Contains(errMsg, "permission denied") ||
		strings.Contains(errMsg, "access denied") ||
		strings.Contains(errMsg, "operation not permitted")
}
