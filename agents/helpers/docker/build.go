package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

type Env struct {
	Key   string
	Value string
}

type BuilderConfiguration struct {
	Root        string
	Dockerfile  string
	Ignorefile  string
	Destination *resources.DockerImage
	Output      io.Writer
}

type Builder struct {
	BuilderConfiguration
}

func IsValidDockerImageName(_ string) bool {
	// Docker image name regex
	return true
}

func NewBuilder(cfg BuilderConfiguration) (*Builder, error) {
	return &Builder{BuilderConfiguration: cfg}, nil
}

type BuilderOutput struct{}

func (builder *Builder) Build(ctx context.Context) (*BuilderOutput, error) {
	w := wool.Get(ctx).In("Builder.Build", wool.DirField(builder.Root))
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot create docker client")
	}

	buildContextBuffer, err := builder.createTarArchive(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create tar archive")
	}
	buildContext := buildContextBuffer.Bytes()

	// Build the Docker image
	imageBuildResponse, err := cli.ImageBuild(
		ctx,
		bytes.NewReader(buildContext),
		types.ImageBuildOptions{
			Dockerfile: builder.Dockerfile,
			Tags:       []string{builder.Destination.FullName()},
			Remove:     true,
		},
	)
	if err != nil {
		return nil, w.Wrapf(err, "cannot build image")
	}

	// Respond the build output
	scanner := bufio.NewScanner(imageBuildResponse.Body)
	var buildOutput struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"errorDetail"`
		Stream string `json:"stream"`
	}
	for scanner.Scan() {
		line := scanner.Bytes()

		if err := json.Unmarshal(line, &buildOutput); err != nil {
			w.Error("cannot unmarshal build output", wool.ErrField(err))
			continue
		}

		if buildOutput.Error != nil {
			w.Error("got build error", wool.Field("output", buildOutput.Error.Message))
		} else {
			out := strings.TrimSpace(buildOutput.Stream)
			if len(out) == 0 {
				continue
			}
			_, _ = builder.Output.Write([]byte(out))

		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading build output: %v\n", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			w.Error("Error closing build response body", wool.ErrField(err))
		}
	}(imageBuildResponse.Body)
	return nil, nil
}

func (builder *Builder) readDockerignore(ctx context.Context) ([]string, error) {
	if builder.Ignorefile == "" {
		return nil, nil
	}
	w := wool.Get(ctx).In("Builder.readDockerignore", wool.DirField(builder.Root))
	ignoreFilePath := filepath.Join(builder.Root, builder.Ignorefile)
	file, err := os.Open(ignoreFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No dockerignore file, nothing to ignore
		}
		return nil, err
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	w.Debug("patterns", wool.Field("patterns", patterns))
	return patterns, nil
}

func shouldIgnore(ctx context.Context, file string, patterns []string) bool {
	w := wool.Get(ctx).In("Builder.shouldIgnore", wool.Field("file", file), wool.Field("patterns", patterns))
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, file)
		if err != nil {
			w.Focus("error", wool.ErrField(err))
			continue // Invalid pattern, skip it
		}
		if matched {
			return true
		}
	}
	return false
}

// createTarArchive creates a tar archive from the provided directory and returns it as a bytes buffer.
func (builder *Builder) createTarArchive(ctx context.Context) (*bytes.Buffer, error) {
	// Add a buffer to write our archive to.
	buf := new(bytes.Buffer)

	// Add a new tar archive.
	tw := tar.NewWriter(buf)

	patterns, err := builder.readDockerignore(ctx)
	if err != nil {
		return nil, err
	}

	// Walk through each file/folder in the path and add it to the tar archive.
	err = filepath.Walk(builder.Root, func(file string, fi os.FileInfo, err error) error {
		// Return any error.
		if err != nil {
			return err
		}

		// Add a new dir/file header.
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(builder.Root, file)
		if err != nil {
			return err
		}

		if shouldIgnore(ctx, rel, patterns) {
			return nil
		}

		header.Name = rel

		// Write the header.
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		// If it's not a directory, write the file content.
		if !fi.Mode().IsDir() {
			data, err := os.Open(file)
			if err != nil {
				return err
			}
			defer data.Close()

			if _, err := io.Copy(tw, data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Make sure to check the error on Stop.
	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}

//func printTarContents(logger *agents.AgentLogger, tarBuffer *bytes.Buffer) {
//	tarReader := tar.NewReader(tarBuffer)
//
//	logger.Debugf("Contents of the tar archive:")
//	for {
//		header, err := tarReader.Next()
//		if err == io.EOF {
//			break // End of archive
//		}
//		if err != nil {
//			logger.Debugf("got error: %v", err)
//			break
//		}
//
//		logger.Debugf("FILE: %v", header.Name)
//	}
//}
//
//func printFileContentFromTar(logger *agents.AgentLogger, tarBuffer *bytes.Buffer, filename string) error {
//	tarReader := tar.NewReader(tarBuffer)
//
//	for {
//		header, err := tarReader.Next()
//		if err == io.EOF {
//			break // End of archive
//		}
//		if err != nil {
//			return fmt.Error("error reading tar header: %v", err)
//		}
//
//		if header.Name == filename {
//			content, err := ioutil.ReadAll(tarReader)
//			if err != nil {
//				return fmt.Error("error reading file content: %v", err)
//			}
//			logger.Debugf("Content of %s:\n%s\n", filename, content)
//			return nil
//		}
//	}
//
//	logger.Debugf("File %s not found in tar archive\n", filename)
//	return nil
//}
