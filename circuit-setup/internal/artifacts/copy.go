package artifacts

import (
	"io"
	"os"
	"path/filepath"
)

// CreateTempInDir creates a temporary file inside the destination directory.
//
// Keeping the temporary file in the same directory as the final destination
// guarantees that the later rename operation is a same-filesystem rename, which
// is required for atomic replacement semantics on POSIX filesystems.
func CreateTempInDir(dir, pattern string) (*os.File, string, error) {
	// Ensure the target directory exists before creating a temporary file. This
	// prevents callers from having to duplicate directory creation logic.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}

	// Delegate unique temporary filename generation to the standard library and
	// return both the file handle and path so callers can rename or clean up.
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, "", err
	}

	return f, f.Name(), nil
}

// CopyFile copies src to dst atomically and fsyncs destination.
//
// The function never writes directly to dst. Instead, it writes to a temporary
// file in the destination directory, fsyncs it, and atomically renames it into
// place. This prevents partial destination files when the process crashes.
func CopyFile(src, dst string) error {
	// Open source first so early permission/path failures are reported before any
	// destination-side mutation happens.
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Create a temporary output file in destination directory so rename remains
	// atomic on the final promotion step.
	out, tmpPath, err := CreateTempInDir(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}

	// Remove temporary output unless the file has been successfully promoted to
	// destination. This guarantees no stale temp files on failure paths.
	keepTmp := true
	defer func() {
		_ = out.Close()
		if keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	// Stream source into temp output to avoid loading artifacts fully in memory.
	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	// Force bytes to disk before rename to reduce risk of metadata-only commits
	// that leave incomplete file contents after abrupt shutdown.
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}

	// Destination now owns the temp path; disable deferred removal.
	keepTmp = false
	return nil
}

// WriteReaderToFileAtomic streams r into dst atomically and fsyncs destination.
//
// This is the generic "reader to atomic file" variant used by download paths
// where data does not originate from an existing source file on disk.
func WriteReaderToFileAtomic(r io.Reader, dst string) error {
	// Create temporary output beside destination so final rename is atomic.
	out, tmpPath, err := CreateTempInDir(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}

	// Keep temp file only until successful rename; otherwise clean it up.
	keepTmp := true
	defer func() {
		_ = out.Close()
		if keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	// Stream request/remote body directly into temp output file.
	if _, err := io.Copy(out, r); err != nil {
		return err
	}

	// Flush data before rename so destination observes durable file contents.
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}

	// Promotion succeeded; destination now references the temp inode.
	keepTmp = false
	return nil
}
