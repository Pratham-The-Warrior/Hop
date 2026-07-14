// Package archive provides tar.gz packaging and extraction for directory transfers.
// When `hop share` receives a directory argument, this package transparently
// compresses it into a .tar.gz archive. On the receiver side, it unpacks the
// archive into the original directory structure.
//
// Security: Path traversal attacks (zip-slip) are prevented by validating all
// extracted paths stay within the destination directory. Symlinks are skipped
// during packaging to prevent symlink-based attacks.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// gzipLevel controls the gzip compression level for directory packaging.
	// Level 6 balances compression ratio and speed well for mixed content.
	gzipLevel = gzip.DefaultCompression

	// readBufSize is the buffer size for streaming file data into the archive.
	// Matches the transfer engine's 1 MB chunk size to keep memory flat.
	readBufSize = 1 << 20 // 1 MB
)

// PackResult holds metadata about a packaged directory.
type PackResult struct {
	ArchivePath string // Path to the temporary .tar.gz file
	FileCount   int    // Number of files included
	TotalSize   int64  // Total uncompressed size of all files
	DirName     string // Base name of the original directory
}

// PackDirectory walks the directory tree at dirPath and creates a temporary
// .tar.gz archive. The archive preserves relative directory structure.
// Symlinks are skipped with a warning. The caller is responsible for cleaning
// up the returned archive file via CleanupArchive.
func PackDirectory(dirPath string) (*PackResult, error) {
	// Resolve to absolute path and verify it's a directory
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", absPath)
	}

	dirName := filepath.Base(absPath)
	parentDir := filepath.Dir(absPath)

	// Create temp file for the archive
	tmpFile, err := os.CreateTemp("", "hop-archive-*.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("creating temp archive: %w", err)
	}
	archivePath := tmpFile.Name()

	// Set up gzip → tar writer chain
	gzWriter, err := gzip.NewWriterLevel(tmpFile, gzipLevel)
	if err != nil {
		tmpFile.Close()
		os.Remove(archivePath)
		return nil, fmt.Errorf("creating gzip writer: %w", err)
	}
	tarWriter := tar.NewWriter(gzWriter)

	result := &PackResult{
		ArchivePath: archivePath,
		DirName:     dirName,
	}

	buf := make([]byte, readBufSize)

	// Walk the directory tree
	err = filepath.Walk(absPath, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Compute relative path from parent so the archive includes the dir name
		relPath, err := filepath.Rel(parentDir, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}
		// Use forward slashes in tar archives for cross-platform compatibility
		relPath = filepath.ToSlash(relPath)

		// Skip symlinks
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return fmt.Errorf("creating tar header for %s: %w", relPath, err)
		}
		header.Name = relPath

		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("writing tar header for %s: %w", relPath, err)
		}

		// If it's a directory, the header is enough
		if fi.IsDir() {
			return nil
		}

		// Write file content
		if fi.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("opening %s: %w", relPath, err)
			}
			defer f.Close()

			written, err := io.CopyBuffer(tarWriter, f, buf)
			if err != nil {
				return fmt.Errorf("writing %s to archive: %w", relPath, err)
			}

			result.FileCount++
			result.TotalSize += written
		}

		return nil
	})

	if err != nil {
		tarWriter.Close()
		gzWriter.Close()
		tmpFile.Close()
		os.Remove(archivePath)
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	// Close writers in order: tar → gzip → file
	if err := tarWriter.Close(); err != nil {
		gzWriter.Close()
		tmpFile.Close()
		os.Remove(archivePath)
		return nil, fmt.Errorf("closing tar writer: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		tmpFile.Close()
		os.Remove(archivePath)
		return nil, fmt.Errorf("closing gzip writer: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(archivePath)
		return nil, fmt.Errorf("closing archive file: %w", err)
	}

	return result, nil
}

// UnpackArchive extracts a .tar.gz archive into destDir.
// The archive should contain a top-level directory that will be
// preserved in the output. All extracted paths are validated to
// prevent directory traversal attacks.
func UnpackArchive(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer f.Close()

	gzReader, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	buf := make([]byte, readBufSize)

	absDestDir, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolving dest dir: %w", err)
	}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}

		// Validate and construct the target path
		targetPath := filepath.Join(absDestDir, filepath.FromSlash(header.Name))
		absTarget, err := filepath.Abs(targetPath)
		if err != nil {
			return fmt.Errorf("resolving target path: %w", err)
		}

		// Security: prevent directory traversal (zip-slip)
		if !strings.HasPrefix(absTarget, absDestDir+string(os.PathSeparator)) && absTarget != absDestDir {
			return fmt.Errorf("path traversal detected: %s escapes %s", header.Name, destDir)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(absTarget, os.FileMode(header.Mode)|0755); err != nil {
				return fmt.Errorf("creating directory %s: %w", header.Name, err)
			}

		case tar.TypeReg:
			// Ensure parent directory exists
			parentDir := filepath.Dir(absTarget)
			if err := os.MkdirAll(parentDir, 0755); err != nil {
				return fmt.Errorf("creating parent dir for %s: %w", header.Name, err)
			}

			outFile, err := os.OpenFile(absTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("creating file %s: %w", header.Name, err)
			}

			if _, err := io.CopyBuffer(outFile, tarReader, buf); err != nil {
				outFile.Close()
				return fmt.Errorf("extracting %s: %w", header.Name, err)
			}

			if err := outFile.Close(); err != nil {
				return fmt.Errorf("closing %s: %w", header.Name, err)
			}

		default:
			// Skip symlinks, devices, etc.
			continue
		}
	}

	return nil
}

// IsArchive checks if a filename indicates a tar.gz archive.
func IsArchive(fileName string) bool {
	return strings.HasSuffix(strings.ToLower(fileName), ".tar.gz")
}

// CleanupArchive removes a temporary archive file.
// Safe to call with an empty path (no-op).
func CleanupArchive(path string) {
	if path != "" {
		os.Remove(path)
	}
}
