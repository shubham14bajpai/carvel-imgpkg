// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cppforlife/go-cli-ui/ui"
	"github.com/cppforlife/go-cli-ui/ui/fakes"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/k14s/difflib"
	"github.com/k14s/imgpkg/pkg/imgpkg/image"
)

func TestPullingAnImage(t *testing.T) {
	img := randomImage(t)
	layers, err := img.Layers()
	if err != nil {
		t.Fatalf("Failed to get layers from created image %s", err)
	}
	layer := layers[0]

	expectedRepo := "foo/bar"
	fakeRegistry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		layerDigest := mustManifest(t, img).Layers[0].Digest
		configPath := fmt.Sprintf("/v2/%s/blobs/%s", expectedRepo, mustConfigName(t, img))
		manifestPath := fmt.Sprintf("/v2/%s/manifests/latest", expectedRepo)
		layerPath := fmt.Sprintf("/v2/%s/blobs/%s", expectedRepo, layerDigest)
		manifestReqCount := 0

		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case configPath:
			w.Write(mustRawConfigFile(t, img))
		case manifestPath:
			manifestReqCount++
			w.Write(mustRawManifest(t, img))
		case layerPath:
			compressed, err := layer.Compressed()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(w, compressed); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			println(fmt.Sprintf("Unexpected path: %v", r.URL.Path))
		}
	}))

	defer fakeRegistry.Close()

	uri, err := url.Parse(fakeRegistry.URL)
	if err != nil {
		t.Fatalf("Unable to get url from test registry %s", err)
	}

	tempPath := os.TempDir()
	workingDir := filepath.Join(tempPath, "pull-image")
	err = os.Mkdir(workingDir, 0700)
	defer Cleanup(workingDir)

	if err != nil {
		t.Fatalf("Failed to create test directory: %s: %s", workingDir, err)
	}

	fakeUI := &fakes.FakeUI{}
	pull := PullOptions{
		ui:         fakeUI,
		ImageFlags: ImageFlags{fmt.Sprintf("%s/%s", uri.Host, expectedRepo)},
		OutputPath: workingDir,
	}

	err = pull.Run()
	if err != nil {
		t.Fatalf("Expected validations not to err, but did %s", err)
	}

	expectedLines := strings.Join([]string{"Pulling image 'LOCAL_REGISTRY/foo/bar@sha256:REPLACED_SHA'\n", "Extracting layer 'sha256:REPLACED_SHA' (1/1)\n"}, "\n")
	actualLines := replaceShaAndRegistryWithConstants(fakeUI, uri.Host)

	logDiff := diffText(expectedLines, actualLines)
	if logDiff != "" {
		t.Fatalf("Expected specific log messages; diff expected...actual:\n%v\n", logDiff)
	}

	outputDir, err := os.Open(workingDir)
	if err != nil {
		t.Fatalf("Failed to open output path directory: %s", err)
	}
	defer outputDir.Close()

	outputFiles, err := outputDir.Readdir(-1)
	if err != nil {
		t.Fatalf("Failed to read files in output path directory: %s", err)
	}
	if len(outputFiles) != 1 {
		t.Fatalf("Incorrect number of files in output path directory. Expected 1, got: %d", len(outputFiles))
	}

	writtenFileName := outputFiles[0].Name()
	if !strings.Contains(writtenFileName, "random_file_") {
		t.Fatalf("Incorrect file name of written image. Got: %s", writtenFileName)
	}

	if outputFiles[0].Size() != 1024 {
		t.Fatalf("Incorrect pulled file size. Expected 1024, got: %d", outputFiles[0].Size())
	}
}

func TestPullingABundle(t *testing.T) {
	adds := make([]mutate.Addendum, 0, 5)
	layer, err := createImgPkgLayer()
	if err != nil {
		t.Fatalf("Unable to create an img pkg layer: %s", err)

	}

	adds = append(adds, mutate.Addendum{
		Layer: layer,
		History: v1.History{
			Author:    "imgpkg",
			CreatedBy: "imgpkg",
			Created:   v1.Time{time.Now()},
		},
	})
	img := empty.Image
	configFile, err := img.ConfigFile()
	configFile.Config.Labels = map[string]string{image.BundleConfigLabel: "true"}

	digest, err := img.Digest()
	println(fmt.Sprintf(">>%v<<", digest))

	img, err = mutate.Append(img, adds...)
	if err != nil {
		t.Fatalf("Unable to append a layer to our test image: %s", err)
	}

	if err != nil {
		t.Fatalf("Unable to get the config file from our test image: %s", err)
	}

	//set the config - cfg.Config.Labels[image.BundleConfigLabel]

	expectedRepo := "foo/bar"
	fakeRegistry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		layerDigest := mustManifest(t, img).Layers[0].Digest
		configPath := fmt.Sprintf("/v2/%s/blobs/%s", expectedRepo, mustConfigName(t, img))
		manifestPath := fmt.Sprintf("/v2/%s/manifests/latest", expectedRepo)
		layerPath := fmt.Sprintf("/v2/%s/blobs/%s", expectedRepo, layerDigest)
		manifestReqCount := 0

		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case configPath:
			w.Write(mustRawConfigFile(t, img))
		case manifestPath:
			manifestReqCount++
			w.Write(mustRawManifest(t, img))
		case layerPath:
			compressed, err := layer.Compressed()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(w, compressed); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			println(fmt.Sprintf("Unexpected path: %v", r.URL.Path))
		}
	}))

	defer fakeRegistry.Close()

	uri, err := url.Parse(fakeRegistry.URL)
	if err != nil {
		t.Fatalf("Unable to get url from test registry %s", err)
	}

	tempPath := os.TempDir()
	workingDir := filepath.Join(tempPath, "pull-bundle")
	err = os.Mkdir(workingDir, 0700)
	defer Cleanup(workingDir)

	if err != nil {
		t.Fatalf("Failed to create test directory: %s: %s", workingDir, err)
	}

	fakeUI := &fakes.FakeUI{}
	pull := PullOptions{
		ui:          fakeUI,
		BundleFlags: BundleFlags{fmt.Sprintf("%s/%s", uri.Host, expectedRepo)},
		OutputPath:  workingDir,
	}

	err = pull.Run()
	if err != nil {
		t.Fatalf("Expected validations not to err, but did %s", err)
	}

	expectedLines := strings.Join([]string{"Pulling image 'LOCAL_REGISTRY/foo/bar@sha256:REPLACED_SHA'\n", "Extracting layer 'sha256:REPLACED_SHA' (1/1)\n"}, "\n")
	actualLines := replaceShaAndRegistryWithConstants(fakeUI, uri.Host)

	logDiff := diffText(expectedLines, actualLines)
	if logDiff != "" {
		t.Fatalf("Expected specific log messages; diff expected...actual:\n%v\n", logDiff)
	}

	outputDir, err := os.Open(workingDir)
	if err != nil {
		t.Fatalf("Failed to open output path directory: %s", err)
	}
	defer outputDir.Close()

	outputFiles, err := outputDir.Readdir(-1)
	if err != nil {
		t.Fatalf("Failed to read files in output path directory: %s", err)
	}
	if len(outputFiles) != 1 {
		t.Fatalf("Incorrect number of files in output path directory. Expected 1, got: %d", len(outputFiles))
	}

	writtenFileName := outputFiles[0].Name()
	if !strings.Contains(writtenFileName, "random_file_") {
		t.Fatalf("Incorrect file name of written image. Got: %s", writtenFileName)
	}

	if outputFiles[0].Size() != 1024 {
		t.Fatalf("Incorrect pulled file size. Expected 1024, got: %d", outputFiles[0].Size())
	}

	//if !strings.Contains(fakeUI.Said, "improved log") {
	//fail
}

func createImgPkgLayer() (v1.Layer, error) {
	// Hash the contents as we write it out to the buffer.
	var b *bytes.Buffer
	hasher := sha256.New()

	tarContents, err := ioutil.ReadFile("test.tar")
	if err != nil {
		return nil, err
	}
	b = bytes.NewBuffer(tarContents)
	hasher.Write(tarContents)

	h := v1.Hash{
		Algorithm: "sha256",
		Hex:       hex.EncodeToString(hasher.Sum(make([]byte, 0, hasher.Size()))),
	}

	return partial.UncompressedToLayer(&uncompressedLayer{
		diffID:    h,
		mediaType: types.DockerLayer,
		content:   b.Bytes(),
	})
}

func TestNoImageOrBundleOrLockError(t *testing.T) {
	pull := PullOptions{}
	err := pull.Run()
	if err == nil {
		t.Fatalf("Expected validations to err, but did not")
	}

	if !strings.Contains(err.Error(), "Expected either image, bundle, or lock") {
		t.Fatalf("Expected error to contain message about invalid flags, got: %s", err)
	}
}

func TestImageAndBundleAndLockError(t *testing.T) {
	pull := PullOptions{ImageFlags: ImageFlags{"image@123456"}, BundleFlags: BundleFlags{"my-bundle"}, LockInputFlags: LockInputFlags{LockFilePath: "lockpath"}}
	err := pull.Run()
	if err == nil {
		t.Fatalf("Expected validations to err, but did not")
	}

	if !strings.Contains(err.Error(), "Expected only one of image, bundle, or lock") {
		t.Fatalf("Expected error to contain message about invalid flags, got: %s", err)
	}
}

func TestInvalidBundleLockKind(t *testing.T) {
	tempDir := os.TempDir()

	workingDir := filepath.Join(tempDir, "imgpkg-pull-units-invalid-kind")
	defer Cleanup(workingDir)
	err := os.MkdirAll(workingDir, 0700)
	if err != nil {
		t.Fatalf("Failed to setup test: %s", err)
	}

	lockFilePath := filepath.Join(workingDir, "bundlelock.yml")
	ioutil.WriteFile(lockFilePath, []byte(`
---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: invalid-value
spec:
  image:
    url: index.docker.io/k8slt/test
    tag: latest
`), 0600)

	pull := PullOptions{LockInputFlags: LockInputFlags{LockFilePath: lockFilePath}}
	err = pull.Run()
	if err == nil {
		t.Fatalf("Expected validations to err, but did not")
	}

	reg := regexp.MustCompile("Invalid `kind` in lockfile at .*imgpkg-pull-units-invalid-kind/bundlelock\\.yml. Expected: BundleLock, got: invalid-value")
	if !reg.MatchString(err.Error()) {
		t.Fatalf("Expected error to contain message about invalid bundlelock kind, got: %s", err)
	}
}

func Test_Invalid_Args_Passed(t *testing.T) {
	confUI := ui.NewConfUI(ui.NewNoopLogger())
	defer confUI.Flush()

	imgpkgCmd := NewDefaultImgpkgCmd(confUI)
	imgpkgCmd.SetArgs([]string{"pull", "k8slt/image", "-o", "/tmp"})
	err := imgpkgCmd.Execute()
	if err == nil {
		t.Fatalf("Expected error from executing imgpkg pull command: %v", err)
	}

	expected := "command 'imgpkg pull' does not accept extra arguments 'k8slt/image'"
	if expected != err.Error() {
		t.Fatalf("\nExpceted: %s\nGot: %s", expected, err.Error())
	}
}

func mustManifest(t *testing.T, img v1.Image) *v1.Manifest {
	m, err := img.Manifest()
	if err != nil {
		t.Fatalf("Manifest() = %v", err)
	}
	return m
}

func mustRawManifest(t *testing.T, img remote.Taggable) []byte {
	m, err := img.RawManifest()
	if err != nil {
		t.Fatalf("RawManifest() = %v", err)
	}
	return m
}

func mustRawConfigFile(t *testing.T, img v1.Image) []byte {
	c, err := img.RawConfigFile()
	if err != nil {
		t.Fatalf("RawConfigFile() = %v", err)
	}
	return c
}

func replaceShaAndRegistryWithConstants(fakeUI *fakes.FakeUI, uriHost string) string {
	replacedLogs := strings.Join(fakeUI.Said, "\n")

	regexCompiled := regexp.MustCompile("sha256:([a-f0-9]{64})")
	replacedLogs = string(regexCompiled.ReplaceAll([]byte(replacedLogs), []byte("sha256:REPLACED_SHA")))
	replacedLogs = strings.ReplaceAll(replacedLogs, uriHost, "LOCAL_REGISTRY")
	return replacedLogs
}

func randomImage(t *testing.T) v1.Image {
	rnd, err := random.Image(1024, 1)
	if err != nil {
		t.Fatalf("random.Image() = %v", err)
	}
	return rnd
}

func mustConfigName(t *testing.T, img v1.Image) v1.Hash {
	h, err := img.ConfigName()
	if err != nil {
		t.Fatalf("ConfigName() = %v", err)
	}
	return h
}

func diffText(left, right string) string {
	var sb strings.Builder

	recs := difflib.Diff(strings.Split(right, "\n"), strings.Split(left, "\n"))

	for _, diff := range recs {
		var mark string

		switch diff.Delta {
		case difflib.RightOnly:
			mark = " + |"
		case difflib.LeftOnly:
			mark = " - |"
		case difflib.Common:
			continue
		}

		// make sure to have line numbers to make sure diff is truly unique
		sb.WriteString(fmt.Sprintf("%3d,%3d%s%s\n", diff.LineLeft, diff.LineRight, mark, diff.Payload))
	}

	return sb.String()
}

// uncompressedLayer implements partial.UncompressedLayer from raw bytes.
type uncompressedLayer struct {
	diffID    v1.Hash
	mediaType types.MediaType
	content   []byte
}

// DiffID implements partial.UncompressedLayer
func (ul *uncompressedLayer) DiffID() (v1.Hash, error) {
	return ul.diffID, nil
}

// Uncompressed implements partial.UncompressedLayer
func (ul *uncompressedLayer) Uncompressed() (io.ReadCloser, error) {
	return ioutil.NopCloser(bytes.NewBuffer(ul.content)), nil
}

// MediaType returns the media type of the layer
func (ul *uncompressedLayer) MediaType() (types.MediaType, error) {
	return ul.mediaType, nil
}
