package archive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackAndUnpackRoundTrip(t *testing.T) {
	// Create a source directory with nested structure
	srcDir := t.TempDir()
	projectDir := filepath.Join(srcDir, "my-project")

	dirs := []string{
		filepath.Join(projectDir, "src"),
		filepath.Join(projectDir, "src", "utils"),
		filepath.Join(projectDir, "docs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("creating dir %s: %v", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(projectDir, "README.md"):          "# My Project\nThis is a test.",
		filepath.Join(projectDir, "src", "main.go"):     "package main\n\nfunc main() {}\n",
		filepath.Join(projectDir, "src", "utils", "helper.go"): "package utils\n\nfunc Help() {}\n",
		filepath.Join(projectDir, "docs", "guide.txt"):  "User guide content here.",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	// Pack the directory
	result, err := PackDirectory(projectDir)
	if err != nil {
		t.Fatalf("PackDirectory: %v", err)
	}
	defer CleanupArchive(result.ArchivePath)

	if result.FileCount != 4 {
		t.Errorf("FileCount = %d, want 4", result.FileCount)
	}
	if result.DirName != "my-project" {
		t.Errorf("DirName = %q, want %q", result.DirName, "my-project")
	}
	if result.TotalSize == 0 {
		t.Error("TotalSize should be > 0")
	}

	// Verify archive file exists
	archiveInfo, err := os.Stat(result.ArchivePath)
	if err != nil {
		t.Fatalf("archive file not found: %v", err)
	}
	if archiveInfo.Size() == 0 {
		t.Error("archive file is empty")
	}

	// Unpack to a new directory
	destDir := t.TempDir()
	if err := UnpackArchive(result.ArchivePath, destDir); err != nil {
		t.Fatalf("UnpackArchive: %v", err)
	}

	// Verify all files were extracted correctly
	for origPath, expectedContent := range files {
		relPath, _ := filepath.Rel(srcDir, origPath)
		extractedPath := filepath.Join(destDir, relPath)

		data, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Errorf("reading extracted file %s: %v", relPath, err)
			continue
		}
		if string(data) != expectedContent {
			t.Errorf("content mismatch for %s: got %q, want %q", relPath, string(data), expectedContent)
		}
	}

	// Verify nested directories exist
	for _, d := range dirs {
		relPath, _ := filepath.Rel(srcDir, d)
		extractedDir := filepath.Join(destDir, relPath)
		info, err := os.Stat(extractedDir)
		if err != nil {
			t.Errorf("extracted dir %s not found: %v", relPath, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s should be a directory", relPath)
		}
	}
}

func TestPackDirectoryNotADir(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file.txt")
	os.WriteFile(filePath, []byte("test"), 0644)

	_, err := PackDirectory(filePath)
	if err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestPackDirectoryNonexistent(t *testing.T) {
	_, err := PackDirectory("/nonexistent/path/dir")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestPackEmptyDirectory(t *testing.T) {
	srcDir := t.TempDir()
	emptyDir := filepath.Join(srcDir, "empty")
	os.MkdirAll(emptyDir, 0755)

	result, err := PackDirectory(emptyDir)
	if err != nil {
		t.Fatalf("PackDirectory: %v", err)
	}
	defer CleanupArchive(result.ArchivePath)

	if result.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", result.FileCount)
	}
	if result.DirName != "empty" {
		t.Errorf("DirName = %q, want %q", result.DirName, "empty")
	}

	// Unpack should succeed and create the directory
	destDir := t.TempDir()
	if err := UnpackArchive(result.ArchivePath, destDir); err != nil {
		t.Fatalf("UnpackArchive: %v", err)
	}

	extractedDir := filepath.Join(destDir, "empty")
	info, err := os.Stat(extractedDir)
	if err != nil {
		t.Fatalf("extracted dir not found: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}
}

func TestIsArchive(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"archive.tar.gz", true},
		{"my-project.tar.gz", true},
		{"ARCHIVE.TAR.GZ", true},
		{"file.zip", false},
		{"file.tar", false},
		{"file.gz", false},
		{"file.txt", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsArchive(tt.name)
		if got != tt.want {
			t.Errorf("IsArchive(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCleanupArchiveNoOp(t *testing.T) {
	// Should not panic on empty path
	CleanupArchive("")

	// Should not panic on nonexistent path
	CleanupArchive("/nonexistent/file.tar.gz")
}

func TestUnpackArchiveNonexistent(t *testing.T) {
	err := UnpackArchive("/nonexistent/file.tar.gz", t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent archive")
	}
}
