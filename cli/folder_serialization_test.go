package cli_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/codefly-dev/core/cli"
	"github.com/stretchr/testify/require"
)

func TestDirectoryRequestRoundTrip(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test_dir")
	require.NoError(t, err)

	defer os.RemoveAll(tempDir)

	// Create a test directory structure
	testFiles := map[string]string{
		"file1.txt":             "Content of file1",
		"file2.go":              "package main\n\nfunc main() {}\n",
		"subdir/file3.json":     `{"key": "value"}`,
		"subdir/file4.txt":      "Content of file4",
		"subdir/ignored.png":    "Binary content",
		"deeply/nested/file.go": "package nested",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(tempDir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
	}

	// Define extensions to include
	extensions := []string{".txt", ".go", ".json"}

	// Create DirectoryRequest
	request, err := cli.SerializeDirectory(tempDir, extensions)
	require.NoError(t, err)

	// Recreate directory from request
	recreatedDir, err := ioutil.TempDir("", "recreated_dir")
	require.NoError(t, err)

	defer os.RemoveAll(recreatedDir)

	err = cli.RecreateDirectory(request, recreatedDir)
	require.NoError(t, err)

	// Compare original and recreated directories
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(tempDir, path)
		if err != nil {
			return err
		}

		recreatedPath := filepath.Join(recreatedDir, relPath)

		if info.IsDir() {
			// Check if directory exists in recreated structure
			if _, err := os.Stat(recreatedPath); os.IsNotExist(err) {
				t.Errorf("Directory not recreated: %s", relPath)
			}
		} else {
			// Check if file should be included based on extension
			if !cli.HasValidExtension(path, extensions) {
				// Check that excluded file doesn't exist in recreated structure
				if _, err := os.Stat(recreatedPath); !os.IsNotExist(err) {
					t.Errorf("Excluded file should not exist: %s", relPath)
				}
			} else {
				// Compare file contents
				originalContent, err := ioutil.ReadFile(path)
				if err != nil {
					return err
				}
				recreatedContent, err := ioutil.ReadFile(recreatedPath)
				if err != nil {
					t.Errorf("Failed to read recreated file: %s", relPath)
					return nil
				}
				if !reflect.DeepEqual(originalContent, recreatedContent) {
					t.Errorf("File contents do not match for: %s", relPath)
				}
			}
		}

		return nil
	})
	require.NoError(t, err)
}
