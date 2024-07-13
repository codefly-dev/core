package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	utils "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
)

// Folder -> Request

// SerializeDirectory walks through the given directory and creates a DirectoryRequest
// containing FileInfo for files with the specified extensions.
func SerializeDirectory(rootPath string, extensions []string) (*utils.Directory, error) {
	request := &utils.Directory{}

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path
		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}

		// Create FileInfo for directories
		if info.IsDir() {
			request.Files = append(request.Files, &utils.FileInfo{
				Path:        relPath,
				IsDirectory: true,
			})
			return nil
		}

		// Check if the file has one of the specified extensions
		if !HasValidExtension(path, extensions) {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Create FileInfo for files
		request.Files = append(request.Files, &utils.FileInfo{
			Path:        relPath,
			Content:     content,
			IsDirectory: false,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory: %v", err)
	}

	return request, nil
}

// HasValidExtension checks if the file has one of the specified extensions
func HasValidExtension(path string, extensions []string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, validExt := range extensions {
		if ext == strings.ToLower(validExt) {
			return true
		}
	}
	return false
}

// Request -> Folder

func RecreateDirectory(request *utils.Directory, destPath string) error {
	for _, file := range request.Files {
		fullPath := filepath.Join(destPath, file.Path)

		if file.IsDirectory {
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(fullPath, file.Content, 0600); err != nil {
				return err
			}
		}
	}
	return nil
}
