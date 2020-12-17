// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/tar"
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

		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case configPath:
			w.Write(mustRawConfigFile(t, img))
		case manifestPath:
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
	// setup test
	manifestFromTheBundleImageLockfile := "fake OCI manifest that is referenced by the bundle's imageLock file"
	dir := createTestBundleOnDisk(manifestFromTheBundleImageLockfile, t)
	defer Cleanup(dir)

	userAndRepoInFakeRegistry := "foo/bar"
	bundleServedByFakeRegistry := createABundleFromDisk(dir, t)

	fakeRegistry := createFakeOCIRegistry(t, bundleServedByFakeRegistry, userAndRepoInFakeRegistry, manifestFromTheBundleImageLockfile)
	defer fakeRegistry.Close()

	uri, err := url.Parse(fakeRegistry.URL)
	if err != nil {
		t.Fatalf("Unable to get url from test registry %s", err)
	}

	fakeUI := &fakes.FakeUI{}
	pull := PullOptions{
		ui:          fakeUI,
		BundleFlags: BundleFlags{fmt.Sprintf("%s/%s", uri.Host, userAndRepoInFakeRegistry)},
		OutputPath:  dir,
	}

	// test subject
	err = pull.Run()
	if err != nil {
		t.Fatalf("Expected validations not to err, but did %s", err)
	}

	// assertions
	expectedLines := strings.Join([]string{"Pulling image 'LOCAL_REGISTRY/foo/bar@sha256:REPLACED_SHA'\n", "Extracting layer 'sha256:REPLACED_SHA' (1/1)\n"}, "\n")
	actualLines := replaceShaAndRegistryWithConstants(fakeUI, uri.Host)

	logDiff := diffText(expectedLines, actualLines)
	if logDiff != "" {
		t.Fatalf("Expected specific log messages; diff expected...actual:\n%v\n", logDiff)
	}

	if !strings.Contains(fakeUI.Said[0], "improved log") {
		t.Fatalf("%v", diffText(fakeUI.Said[0], "improved log"))
	}
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

func createFakeOCIRegistry(t *testing.T, img v1.Image, expectedRepo string, manifestFromTheBundleImageLockfile string) *httptest.Server {
	fakeRegistry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		layerDigest := mustManifest(t, img).Layers[0].Digest
		configPath := fmt.Sprintf("/v2/%s/blobs/%s", expectedRepo, mustConfigName(t, img))
		manifestPath := fmt.Sprintf("/v2/%s/manifests/latest", expectedRepo)
		layerPath := fmt.Sprintf("/v2/%s/blobs/%s", expectedRepo, layerDigest)

		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case configPath:
			w.Write(mustRawConfigFile(t, img))
		case manifestPath:
			w.Write(mustRawManifest(t, img))
		case layerPath:
			layers, err := img.Layers()
			if err != nil {
				t.Fatal(err)
			}
			compressed, err :=  layers[0].Compressed()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(w, compressed); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.Write([]byte(manifestFromTheBundleImageLockfile))
		}
	}))
	return fakeRegistry
}

func createTestBundleOnDisk(manifestFromTheBundleImageLockfile string, t *testing.T) string {
	sha256OfManifestFromTheBundleImageLockfile, _, err := v1.SHA256(bytes.NewReader([]byte(manifestFromTheBundleImageLockfile)))
	if err != nil {
		t.Fatalf("Unable to generate digest needed for images.yml: %s", err)
	}

	dir, err := ioutil.TempDir(os.TempDir(), "testingpullingabundle")
	err = os.Mkdir(filepath.Join(dir, ".imgpkg"), os.ModePerm)
	if err != nil {
		t.Fatalf("Unable to create the .imgpkg directory: %s", err)
	}

	err = ioutil.WriteFile(filepath.Join(dir, ".imgpkg", "images.yml"), []byte(fmt.Sprintf(`apiVersion: ""
kind: ""
spec:
  images:
  - annotations: null
    image: index.docker.io/fake/a@%s`, sha256OfManifestFromTheBundleImageLockfile)), os.ModePerm)
	if err != nil {
		t.Fatalf("Unable to create the images.yml file: %s", err)
	}
	return dir
}

func createABundleFromDisk(dir string, t *testing.T) v1.Image {
	layer, err := createImgPkgLayer(dir)
	if err != nil {
		t.Fatalf("Unable to create an img pkg layer: %s", err)
	}

	img := empty.Image
	img, err = mutate.Append(img, mutate.Addendum{
		Layer: layer,
		History: v1.History{
			Author:    "imgpkg",
			CreatedBy: "imgpkg",
			Created:   v1.Time{time.Now()},
		},
	})
	if err != nil {
		t.Fatalf("Unable to append a layer to our test image: %s", err)
	}
	img, err = mutate.Config(img, v1.Config{
		Labels: map[string]string{image.BundleConfigLabel: "true"},
	})
	if err != nil {
		t.Fatalf("Unable to add labels to our test image: %s", err)
	}
	return img
}

func createImgPkgLayer(directoryToBundle string) (v1.Layer, error) {
	hasher := sha256.New()
	var buf = &bytes.Buffer{}
	compress(directoryToBundle, buf)
	hasher.Write(buf.Bytes())
	h := v1.Hash{
		Algorithm: "sha256",
		Hex:       hex.EncodeToString(hasher.Sum(make([]byte, 0, hasher.Size()))),
	}
	return partial.UncompressedToLayer(&uncompressedLayer{
		diffID:    h,
		mediaType: types.DockerLayer,
		content:   buf.Bytes(),
	})
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

func (ul *uncompressedLayer) DiffID() (v1.Hash, error) {
	return ul.diffID, nil
}

func (ul *uncompressedLayer) Uncompressed() (io.ReadCloser, error) {
	return ioutil.NopCloser(bytes.NewBuffer(ul.content)), nil
}

func (ul *uncompressedLayer) MediaType() (types.MediaType, error) {
	return ul.mediaType, nil
}

func compress(src string, buf io.Writer) error {
	tw := tar.NewWriter(buf)

	// walk through every file in the folder
	filepath.Walk(src, func(file string, fi os.FileInfo, err error) error {
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, file)
		if err != nil {
			return err
		}

		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !fi.IsDir() {
			data, err := os.Open(file)
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, data); err != nil {
				return err
			}
		}
		return nil
	})

	// produce tar
	if err := tw.Close(); err != nil {
		return err
	}

	return nil
}
