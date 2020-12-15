// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
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

	"github.com/cppforlife/go-cli-ui/ui/fakes"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/k14s/difflib"

	"github.com/cppforlife/go-cli-ui/ui"
)

func TestPullingAnImage(t *testing.T) {
	img := randomImage(t)
	layerDigest := mustManifest(t, img).Layers[0].Digest
	layer, err := img.LayerByDigest(layerDigest)

	fakeRegistry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedRepo := "foo/bar"

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
	workingDir := filepath.Join(tempPath, "rewrite-lock")
	fileHandle, err := os.Create(workingDir)
	defer Cleanup(workingDir)

	if err != nil {
		t.Fatalf("Failed to create test directory: %s: %s", workingDir, err)
	}

	fakeUI := &fakes.FakeUI{}
	pull := PullOptions{
		ui:         fakeUI,
		ImageFlags: ImageFlags{fmt.Sprintf("%s/foo/bar", uri.Host)},
		OutputPath: fileHandle.Name(),
	}

	err = pull.Run()
	if err != nil {
		t.Fatalf("Expected validations not to err, but did %s", err)
	}

	expectedLines := strings.Join([]string{"Pulling image 'LOCAL_REGISTRY/foo/bar@sha256:REPLACED_SHA'\n", "Extracting layer 'sha256:REPLACED_SHA' (1/1)\n"}, "\n")
	actualLines := replaceShaAndRegistryWithConstants(fakeUI, uri)

	logDiff := diffText(expectedLines, actualLines)
	if logDiff != "" {
		t.Fatalf("Expected specific log messages; diff expected...actual:\n%v\n", logDiff)
	}

	// backfill that pull 'writes' the contents of the random image to disk. inspect '/tmp/test-bundle'

	//if !strings.Contains(fakeUI.Said, "improved log") {
	//fail
}

func replaceShaAndRegistryWithConstants(fakeUI *fakes.FakeUI, uri *url.URL) string {
	actualLines := strings.Join(fakeUI.Said, "\n")

	regexCompiled := regexp.MustCompile("sha256:([a-f0-9]{64})")
	replacedActualLines := regexCompiled.ReplaceAll([]byte(actualLines), []byte("sha256:REPLACED_SHA"))
	actualLines = strings.ReplaceAll(string(replacedActualLines), uri.Host, "LOCAL_REGISTRY")
	return actualLines
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
