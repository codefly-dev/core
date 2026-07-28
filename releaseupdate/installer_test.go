package releaseupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInstallerStagesAndAtomicallyAppliesVerifiedExecutable(t *testing.T) {
	executablePath, err := os.Executable()
	require.NoError(t, err)
	executable, err := os.ReadFile(executablePath)
	require.NoError(t, err)

	scenario := newInstallScenario(t, platformAssetName(), executable)
	defer scenario.close()

	staged, err := scenario.installer.StageAndVerify(context.Background(), scenario.release, scenario.destination)
	require.NoError(t, err)
	stagedInfo, err := os.Stat(staged.Path())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), stagedInfo.Mode().Perm())

	require.NoError(t, staged.Apply(context.Background()))
	installed, err := os.ReadFile(scenario.destination.Path)
	require.NoError(t, err)
	require.Equal(t, executable, installed)
	installedInfo, err := os.Stat(scenario.destination.Path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o751), installedInfo.Mode().Perm())
	require.ErrorIs(t, staged.Apply(context.Background()), ErrStagedUpdateUsed)
	require.NoFileExists(t, filepath.Join(filepath.Dir(scenario.destination.Path), "."+filepath.Base(scenario.destination.Path)+".old"))
	requireNoStagingFiles(t, scenario.destination.StagingDirectory)
}

func TestInstallerFailsClosedBeforeDestinationMutation(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*installScenario)
		expected    error
		installKind InstallKind
	}{
		{
			name: "corrupt artifact",
			configure: func(scenario *installScenario) {
				corrupt := append([]byte(nil), scenario.responses[scenario.release.Asset.Name]...)
				corrupt[0] ^= 0xff
				scenario.responses[scenario.release.Asset.Name] = corrupt
			},
			expected:    ErrChecksum,
			installKind: InstallKindDirect,
		},
		{
			name: "wrong publisher key",
			configure: func(scenario *installScenario) {
				wrongKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				require.NoError(t, err)
				digest := sha256.Sum256(scenario.responses["checksums.txt"])
				signature, err := ecdsa.SignASN1(rand.Reader, wrongKey, digest[:])
				require.NoError(t, err)
				scenario.setResponse("checksums.txt.sig", signature)
			},
			expected:    ErrAuthenticity,
			installKind: InstallKindDirect,
		},
		{
			name: "unsigned metadata",
			configure: func(scenario *installScenario) {
				scenario.release.Assets = scenario.release.Assets[:2]
			},
			expected:    ErrAssetNotFound,
			installKind: InstallKindDirect,
		},
		{
			name: "wrong platform",
			configure: func(scenario *installScenario) {
				scenario.release.Asset.Platform.Arch = "wrong"
			},
			expected:    ErrUnsupportedPlatform,
			installKind: InstallKindDirect,
		},
		{
			name:        "homebrew owned",
			configure:   func(*installScenario) {},
			expected:    ErrInstallNotOwned,
			installKind: InstallKindHomebrew,
		},
		{
			name:        "tauri owned",
			configure:   func(*installScenario) {},
			expected:    ErrInstallNotOwned,
			installKind: InstallKindTauri,
		},
		{
			name:        "managed",
			configure:   func(*installScenario) {},
			expected:    ErrInstallNotOwned,
			installKind: InstallKindManaged,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := newInstallScenario(t, platformAssetName(), []byte("published artifact"))
			defer scenario.close()
			scenario.destination.InstallKind = test.installKind
			test.configure(scenario)

			before, err := os.ReadFile(scenario.destination.Path)
			require.NoError(t, err)
			beforeInfo, err := os.Stat(scenario.destination.Path)
			require.NoError(t, err)

			_, err = scenario.installer.StageAndVerify(context.Background(), scenario.release, scenario.destination)
			require.ErrorIs(t, err, test.expected)
			after, readErr := os.ReadFile(scenario.destination.Path)
			require.NoError(t, readErr)
			require.Equal(t, before, after)
			afterInfo, statErr := os.Stat(scenario.destination.Path)
			require.NoError(t, statErr)
			require.Equal(t, beforeInfo.Mode().Perm(), afterInfo.Mode().Perm())
			requireNoStagingFiles(t, scenario.destination.StagingDirectory)
		})
	}
}

func TestInstallerRejectsPathTraversalAndCorruptArchives(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		artifact []byte
		expected error
	}{
		{
			name:     "tar path traversal",
			filename: "tool_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz",
			artifact: tarGzip(t, "../tool", []byte("payload")),
			expected: ErrUnsafeArchive,
		},
		{
			name:     "corrupt zip",
			filename: "tool_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip",
			artifact: []byte("not a zip archive"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := newInstallScenario(t, test.filename, test.artifact)
			defer scenario.close()
			before, err := os.ReadFile(scenario.destination.Path)
			require.NoError(t, err)

			_, err = scenario.installer.StageAndVerify(context.Background(), scenario.release, scenario.destination)
			require.Error(t, err)
			if test.expected != nil {
				require.ErrorIs(t, err, test.expected)
			}
			after, readErr := os.ReadFile(scenario.destination.Path)
			require.NoError(t, readErr)
			require.Equal(t, before, after)
			requireNoStagingFiles(t, scenario.destination.StagingDirectory)
		})
	}
}

func TestInstallerBoundsDownloadsBeforeMutation(t *testing.T) {
	scenario := newInstallScenario(t, platformAssetName(), []byte("artifact is larger than the configured bound"))
	defer scenario.close()

	installer, err := scenario.newInstaller(InstallerOptions{MaxArtifactBytes: 4})
	require.NoError(t, err)
	before, err := os.ReadFile(scenario.destination.Path)
	require.NoError(t, err)

	_, err = installer.StageAndVerify(context.Background(), scenario.release, scenario.destination)
	require.ErrorIs(t, err, ErrDownloadLimit)
	after, readErr := os.ReadFile(scenario.destination.Path)
	require.NoError(t, readErr)
	require.Equal(t, before, after)
	requireNoStagingFiles(t, scenario.destination.StagingDirectory)
}

func TestCancelledDownloadLeavesNoPartialArtifact(t *testing.T) {
	scenario := newInstallScenario(t, platformAssetName(), bytes.Repeat([]byte("x"), 1<<20))
	defer scenario.close()
	scenario.interruptArtifact = true

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := scenario.installer.StageAndVerify(ctx, scenario.release, scenario.destination)
		result <- err
	}()
	select {
	case <-scenario.artifactStarted:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("artifact download did not start")
	}
	err := <-result
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	requireNoStagingFiles(t, scenario.destination.StagingDirectory)
	installed, readErr := os.ReadFile(scenario.destination.Path)
	require.NoError(t, readErr)
	require.Equal(t, []byte("current executable"), installed)
}

func TestInstallerDoesNotExposeCredentialsOrSignedRedirects(t *testing.T) {
	scenario := newInstallScenario(t, platformAssetName(), []byte("artifact"))
	defer scenario.close()
	scenario.redirectAsset = "checksums.txt"
	scenario.redirectLocation = "https://not-allowed.invalid/download?signature=signed-url-secret"

	installer, err := scenario.newInstaller(InstallerOptions{Token: "bearer-token-secret"})
	require.NoError(t, err)
	_, err = installer.StageAndVerify(context.Background(), scenario.release, scenario.destination)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "bearer-token-secret")
	require.NotContains(t, err.Error(), "signed-url-secret")
	require.NotContains(t, err.Error(), scenario.redirectLocation)
}

func TestStagedUpdateRefusesChangedDestination(t *testing.T) {
	executablePath, err := os.Executable()
	require.NoError(t, err)
	executable, err := os.ReadFile(executablePath)
	require.NoError(t, err)
	scenario := newInstallScenario(t, platformAssetName(), executable)
	defer scenario.close()

	staged, err := scenario.installer.StageAndVerify(context.Background(), scenario.release, scenario.destination)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(scenario.destination.Path, []byte("changed by owner"), 0o751))

	err = staged.Apply(context.Background())
	require.ErrorIs(t, err, ErrInvalidDestination)
	current, readErr := os.ReadFile(scenario.destination.Path)
	require.NoError(t, readErr)
	require.Equal(t, []byte("changed by owner"), current)
	require.NoError(t, staged.Discard())
}

type installScenario struct {
	t                 *testing.T
	server            *httptest.Server
	responses         map[string][]byte
	release           Release
	destination       Destination
	installer         Installer
	certificate       []byte
	mutex             sync.Mutex
	requestCount      atomic.Int64
	interruptArtifact bool
	artifactStarted   chan struct{}
	artifactStartOnce sync.Once
	redirectAsset     string
	redirectLocation  string
}

func newInstallScenario(t *testing.T, assetName string, artifact []byte) *installScenario {
	t.Helper()
	key, certificate := publisherIdentity(t)
	checksum := sha256.Sum256(artifact)
	checksums := []byte(hex.EncodeToString(checksum[:]) + "  " + assetName + "\n")
	checksumsDigest := sha256.Sum256(checksums)
	signature, err := ecdsa.SignASN1(rand.Reader, key, checksumsDigest[:])
	require.NoError(t, err)

	scenario := &installScenario{
		t:               t,
		responses:       map[string][]byte{assetName: artifact, "checksums.txt": checksums, "checksums.txt.sig": signature},
		certificate:     certificate,
		artifactStarted: make(chan struct{}),
	}
	scenario.server = httptest.NewTLSServer(http.HandlerFunc(scenario.serve))

	assets := []Asset{
		{ID: 1, Name: assetName, DownloadURL: scenario.server.URL + "/" + assetName, Size: int64(len(artifact))},
		{ID: 2, Name: "checksums.txt", DownloadURL: scenario.server.URL + "/checksums.txt", Size: int64(len(checksums))},
		{ID: 3, Name: "checksums.txt.sig", DownloadURL: scenario.server.URL + "/checksums.txt.sig", Size: int64(len(signature))},
	}
	scenario.release = Release{
		ID:      1,
		Version: mustVersion(t, "1.1.0"),
		Asset:   assets[0],
		Assets:  assets,
	}
	scenario.release.Asset.Platform = CurrentPlatform()

	directory := t.TempDir()
	target := filepath.Join(directory, "tool")
	require.NoError(t, os.WriteFile(target, []byte("current executable"), 0o751))
	scenario.destination = Destination{
		Path:             target,
		StagingDirectory: directory,
		InstallKind:      InstallKindDirect,
	}
	scenario.installer, err = scenario.newInstaller(InstallerOptions{})
	require.NoError(t, err)
	return scenario
}

func (s *installScenario) serve(response http.ResponseWriter, request *http.Request) {
	s.requestCount.Add(1)
	name := strings.TrimPrefix(request.URL.Path, "/")
	s.mutex.Lock()
	body, found := s.responses[name]
	redirectAsset := s.redirectAsset
	redirectLocation := s.redirectLocation
	interrupt := s.interruptArtifact && name == s.release.Asset.Name
	s.mutex.Unlock()
	if !found {
		http.NotFound(response, request)
		return
	}
	if name == redirectAsset {
		response.Header().Set("Location", redirectLocation)
		response.WriteHeader(http.StatusFound)
		return
	}
	response.Header().Set("Content-Length", big.NewInt(int64(len(body))).String())
	if interrupt {
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(body[:1024])
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		s.artifactStartOnce.Do(func() { close(s.artifactStarted) })
		<-request.Context().Done()
		return
	}
	_, _ = response.Write(body)
}

func (s *installScenario) setResponse(name string, data []byte) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.responses[name] = data
	for index := range s.release.Assets {
		if s.release.Assets[index].Name == name {
			s.release.Assets[index].Size = int64(len(data))
			if s.release.Asset.Name == name {
				s.release.Asset.Size = int64(len(data))
			}
		}
	}
}

func (s *installScenario) newInstaller(overrides InstallerOptions) (Installer, error) {
	options := InstallerOptions{
		HTTPClient:              s.server.Client(),
		PublisherCertificatePEM: s.certificate,
		ChecksumsAssetName:      "checksums.txt",
		AllowedDownloadHosts:    []string{strings.TrimPrefix(s.server.URL, "https://")},
		MaxArtifactBytes:        overrides.MaxArtifactBytes,
		MaxMetadataBytes:        overrides.MaxMetadataBytes,
		MaxExecutableBytes:      overrides.MaxExecutableBytes,
		MaxArchiveBytes:         overrides.MaxArchiveBytes,
		Token:                   overrides.Token,
	}
	return NewInstaller(options)
}

func (s *installScenario) close() {
	s.server.Close()
}

func publisherIdentity(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Codefly release test publisher"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(4102444800, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func platformAssetName() string {
	return "tool_" + runtime.GOOS + "_" + runtime.GOARCH
}

func requireNoStagingFiles(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".releaseupdate-*"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func tarGzip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}))
	_, err := io.Copy(tarWriter, bytes.NewReader(data))
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	return buffer.Bytes()
}

func TestArchiveExpandedSizeIsBounded(t *testing.T) {
	archive := tarGzip(t, "tool", bytes.Repeat([]byte("x"), 4096))
	err := validateArchive(context.Background(), archive, "tool_linux_amd64.tar.gz", 1024)
	require.ErrorIs(t, err, ErrDownloadLimit)
}

func TestInstallerRequiresPinnedPublisherIdentity(t *testing.T) {
	_, err := NewInstaller(InstallerOptions{
		PublisherCertificatePEM: []byte("not a certificate"),
		ChecksumsAssetName:      "checksums.txt",
		AllowedDownloadHosts:    []string{"github.com"},
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrChecksum))
}
