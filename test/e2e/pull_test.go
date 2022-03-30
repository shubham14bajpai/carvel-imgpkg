// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware-tanzu/carvel-imgpkg/pkg/imgpkg/bundle"
	"github.com/vmware-tanzu/carvel-imgpkg/pkg/imgpkg/lockconfig"
	"github.com/vmware-tanzu/carvel-imgpkg/test/helpers"
)

func TestPullImageLockRewrite(t *testing.T) {
	logger := &helpers.Logger{}

	env := helpers.BuildEnv(t)
	imgpkg := helpers.Imgpkg{T: t, L: helpers.Logger{}, ImgpkgPath: env.ImgpkgPath}
	defer env.Cleanup()

	imageDigestRef := "@sha256:ebf526c198a14fa138634b9746c50ec38077ec9b3986227e79eb837d26f59dc6"
	dockerhubImgRef := helpers.CompleteImageRef("library/hello-world")
	imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s%s
`, dockerhubImgRef, imageDigestRef)

	bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, imageLockYAML)

	out := imgpkg.Run([]string{"push", "--tty", "-b", env.Image, "-f", bundleDir})
	bundleDigest := fmt.Sprintf("@%s", helpers.ExtractDigest(t, out))

	imgpkg.Run([]string{"copy", "-b", env.Image, "--to-repo", env.Image})

	pullDir := env.Assets.CreateTempFolder("pull-rewrite-lock")
	imgpkg.Run([]string{"pull", "-b", env.Image, "-o", pullDir})

	expectedImageRef := env.Image + imageDigestRef
	env.Assert.AssertImagesLock(filepath.Join(pullDir, ".imgpkg", "images.yml"), []lockconfig.ImageRef{{Image: expectedImageRef}})

	hash, err := v1.NewHash(bundleDigest[1:])
	require.NoError(t, err)
	locationImg := fmt.Sprintf("%s:%s-%s.image-locations.imgpkg", env.Image, hash.Algorithm, hash.Hex)

	logger.Section("download the locations file and check it", func() {
		locationImgFolder := env.Assets.CreateTempFolder("locations-img")
		env.ImageFactory.Download(locationImg, locationImgFolder)

		locationsFilePath := filepath.Join(locationImgFolder, "image-locations.yml")
		require.FileExists(t, locationsFilePath)

		cfg, err := bundle.NewLocationConfigFromPath(locationsFilePath)
		require.NoError(t, err)

		require.Equal(t, bundle.ImageLocationsConfig{
			APIVersion: "imgpkg.carvel.dev/v1alpha1",
			Kind:       "ImageLocations",
			Images: []bundle.ImageLocation{{
				Image: dockerhubImgRef + imageDigestRef,
				// Repository not used for now because all images will be present in the same repository
				IsBundle: false,
			}},
		}, cfg)
	})

}

func TestPullImageLockRewriteBundleOfBundles(t *testing.T) {
	env := helpers.BuildEnv(t)
	logger := helpers.Logger{}
	imgpkg := helpers.Imgpkg{T: t, L: helpers.Logger{}, ImgpkgPath: env.ImgpkgPath}
	defer env.Cleanup()

	bundleDigestRef := ""
	imageDigestRef := "@sha256:ebf526c198a14fa138634b9746c50ec38077ec9b3986227e79eb837d26f59dc6"
	dockerhubImgRef := helpers.CompleteImageRef("library/hello-world")
	imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s%s
`, dockerhubImgRef, imageDigestRef)

	bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, imageLockYAML)
	uniqueImageName := env.Image + fmt.Sprintf("%d", time.Now().Unix())
	logger.Section("create inner bundle", func() {
		out := imgpkg.Run([]string{"push", "--tty", "-b", uniqueImageName, "-f", bundleDir})
		bundleDigestRef = helpers.ExtractDigest(t, out)
	})

	logger.Section("create new bundle with bundles", func() {
		imagesLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
`, fmt.Sprintf("%s@%s", uniqueImageName, bundleDigestRef))
		env.BundleFactory.AddFileToBundle(filepath.Join(".imgpkg", "images.yml"), imagesLockYAML)

		outerBundleOut := imgpkg.Run([]string{"push", "--tty", "-b", uniqueImageName, "-f", bundleDir})
		outerBundleDigestRef := helpers.ExtractDigest(t, outerBundleOut)

		imgpkg.Run([]string{"copy", "-b", uniqueImageName + "@" + outerBundleDigestRef, "--to-repo", uniqueImageName})

		outDir := env.Assets.CreateTempFolder("bundle-annotation")

		imgpkg.Run([]string{"pull", "--recursive", "-b", uniqueImageName, "-o", outDir})

		subBundleDirectoryPath := strings.ReplaceAll(bundleDigestRef, "sha256:", "sha256-")
		assert.DirExists(t, filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath))
		assert.FileExists(t, filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath, ".imgpkg", "images.yml"))
		assert.FileExists(t, filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath, ".imgpkg", "bundle.yml"))

		innerBundleImagesYmlContent, err := os.ReadFile(filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath, ".imgpkg", "images.yml"))
		assert.NoError(t, err)

		assert.Regexp(t, fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
images:
- image: %s
kind: ImagesLock
`, uniqueImageName+imageDigestRef), string(innerBundleImagesYmlContent))
	})
}

func TestPullBundleOfBundles(t *testing.T) {
	env := helpers.BuildEnv(t)
	logger := helpers.Logger{}
	imgpkg := helpers.Imgpkg{T: t, L: helpers.Logger{}, ImgpkgPath: env.ImgpkgPath}
	defer env.Cleanup()

	bundleDigestRef := ""
	bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, helpers.ImagesYAML)
	logger.Section("create inner bundle", func() {
		out := imgpkg.Run([]string{"push", "--tty", "-b", env.Image, "-f", bundleDir})
		bundleDigestRef = helpers.ExtractDigest(t, out)
	})

	logger.Section("create new bundle with bundles", func() {
		imagesLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
`, fmt.Sprintf("%s@%s", env.Image, bundleDigestRef))
		env.BundleFactory.AddFileToBundle(filepath.Join(".imgpkg", "images.yml"), imagesLockYAML)

		imgpkg.Run([]string{"push", "-b", env.Image, "-f", bundleDir})

		outDir := env.Assets.CreateTempFolder("bundle-annotation")

		imgpkg.Run([]string{"pull", "--recursive", "-b", env.Image, "-o", outDir})

		subBundleDirectoryPath := strings.ReplaceAll(bundleDigestRef, "sha256:", "sha256-")
		assert.DirExists(t, filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath))
		assert.FileExists(t, filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath, ".imgpkg", "images.yml"))
		assert.FileExists(t, filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath, ".imgpkg", "bundle.yml"))

		innerBundleImagesYmlContent, err := os.ReadFile(filepath.Join(outDir, ".imgpkg", "bundles", subBundleDirectoryPath, ".imgpkg", "images.yml"))
		assert.NoError(t, err)
		assert.Equal(t, helpers.ImagesYAML, string(innerBundleImagesYmlContent))
	})
}

func TestPullImageFromSlowServerShouldTimeout(t *testing.T) {
	logger := &helpers.Logger{}

	env := helpers.BuildEnv(t)
	imgpkg := helpers.Imgpkg{T: t, L: helpers.Logger{}, ImgpkgPath: env.ImgpkgPath}
	defer env.Cleanup()

	registry := helpers.NewFakeRegistry(t, logger)
	image := registry.WithRandomImage("random-image")
	registry.Build()
	defer registry.ResetHandler()

	registry.WithCustomHandler(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(5 * time.Second)
	})

	actualErrOut := bytes.NewBufferString("")
	outDir := env.Assets.CreateTempFolder("bundle-annotation")
	imgpkg.RunWithOpts([]string{"pull", "--registry-response-header-timeout", "1s", "-i", image.RefDigest, "-o", outDir}, helpers.RunOpts{
		AllowError:   true,
		StdoutWriter: actualErrOut,
		StderrWriter: actualErrOut,
	})

	assert.Contains(t, actualErrOut.String(), "timeout awaiting response headers")
}

func TestPullImageIndexShouldError(t *testing.T) {
	logger := &helpers.Logger{}

	env := helpers.BuildEnv(t)
	imgpkg := helpers.Imgpkg{T: t, L: helpers.Logger{}, ImgpkgPath: env.ImgpkgPath}
	defer env.Cleanup()

	registry := helpers.NewFakeRegistry(t, logger)
	imageIndex := registry.WithARandomImageIndex("random-image-index", 3)
	registry.Build()
	defer registry.ResetHandler()

	pullDir := env.Assets.CreateTempFolder("pull-rewrite-lock")
	out := bytes.NewBufferString("")
	_, err := imgpkg.RunWithOpts([]string{"pull", "--tty", "-i", imageIndex.RefDigest, "-o", pullDir}, helpers.RunOpts{
		AllowError:   true,
		StderrWriter: out,
		StdoutWriter: out,
	})

	assert.Error(t, err)
	assert.Contains(t, out.String(), "Unable to pull non-images, such as image indexes. (hint: provide a specific digest to the image instead)")
}

func TestPull(t *testing.T) {
	logger := &helpers.Logger{}

	env := helpers.BuildEnv(t)
	imgpkg := helpers.Imgpkg{T: t, L: *logger, ImgpkgPath: env.ImgpkgPath}
	defer env.Cleanup()

	t.Run("Image - copies the User Permission to group and other", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping test as this is a known issue: https://github.com/vmware-tanzu/carvel-imgpkg/issues/270")
		}

		folder := env.Assets.CreateTempFolder("simple-image")
		env.Assets.AddFileToFolderWithPermissions(filepath.Join(folder, "all-on-user-only"), "some text", 0755)
		env.Assets.AddFileToFolderWithPermissions(filepath.Join(folder, "read-on-user-only"), "some text", 0455)
		env.Assets.AddFileToFolderWithPermissions(filepath.Join(folder, "read-write-on-user-only"), "some text", 0655)

		out := imgpkg.Run([]string{"push", "--tty", "-i", env.Image, "-f", folder})
		imgDigest := fmt.Sprintf("@%s", helpers.ExtractDigest(t, out))

		pullDir := env.Assets.CreateTempFolder("pull-dir-simple-image")
		imageRef := fmt.Sprintf("%s%s", env.Image, imgDigest)
		imgpkg.Run([]string{"pull", "-i", imageRef, "-o", pullDir})

		info, err := os.Stat(filepath.Join(pullDir, "all-on-user-only"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0700).String(), (info.Mode() & 0700).String(), "user permission doesnt match")
		assert.Equal(t, os.FileMode(0070).String(), (info.Mode() & 0070).String(), "group permission doesnt match")
		assert.Equal(t, os.FileMode(0007).String(), (info.Mode() & 0007).String(), "other permission doesnt match")
		info, err = os.Stat(filepath.Join(pullDir, "read-on-user-only"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0400).String(), (info.Mode() & 0700).String(), "user permission doesnt match")
		assert.Equal(t, os.FileMode(0040).String(), (info.Mode() & 0070).String(), "group permission doesnt match")
		assert.Equal(t, os.FileMode(0004).String(), (info.Mode() & 0007).String(), "other permission doesnt match")
		info, err = os.Stat(filepath.Join(pullDir, "read-write-on-user-only"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600).String(), (info.Mode() & 0700).String(), "user permission doesnt match")
		assert.Equal(t, os.FileMode(0060).String(), (info.Mode() & 0070).String(), "group permission doesnt match")
		assert.Equal(t, os.FileMode(0006).String(), (info.Mode() & 0007).String(), "other permission doesnt match")
	})

	t.Run("Bundle - copies the User Permission to group and other", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping test as this is a known issue: https://github.com/vmware-tanzu/carvel-imgpkg/issues/270")
		}

		bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, helpers.ImagesYAML)
		env.Assets.AddFileToFolderWithPermissions(filepath.Join(bundleDir, "all-on-user-only"), "some text", 0755)
		env.Assets.AddFileToFolderWithPermissions(filepath.Join(bundleDir, "read-on-user-only"), "some text", 0455)
		env.Assets.AddFileToFolderWithPermissions(filepath.Join(bundleDir, "read-write-on-user-only"), "some text", 0655)

		out := imgpkg.Run([]string{"push", "--tty", "-b", env.Image, "-f", bundleDir})
		imgDigest := fmt.Sprintf("@%s", helpers.ExtractDigest(t, out))

		pullDir := env.Assets.CreateTempFolder("pull-dir-simple-image")
		imageRef := fmt.Sprintf("%s%s", env.Image, imgDigest)
		imgpkg.Run([]string{"pull", "-b", imageRef, "-o", pullDir})

		info, err := os.Stat(filepath.Join(pullDir, "all-on-user-only"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0700).String(), (info.Mode() & 0700).String(), "user permission doesnt match")
		assert.Equal(t, os.FileMode(0070).String(), (info.Mode() & 0070).String(), "group permission doesnt match")
		assert.Equal(t, os.FileMode(0007).String(), (info.Mode() & 0007).String(), "other permission doesnt match")
		info, err = os.Stat(filepath.Join(pullDir, "read-on-user-only"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0400).String(), (info.Mode() & 0700).String(), "user permission doesnt match")
		assert.Equal(t, os.FileMode(0040).String(), (info.Mode() & 0070).String(), "group permission doesnt match")
		assert.Equal(t, os.FileMode(0004).String(), (info.Mode() & 0007).String(), "other permission doesnt match")
		info, err = os.Stat(filepath.Join(pullDir, "read-write-on-user-only"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600).String(), (info.Mode() & 0700).String(), "user permission doesnt match")
		assert.Equal(t, os.FileMode(0060).String(), (info.Mode() & 0070).String(), "group permission doesnt match")
		assert.Equal(t, os.FileMode(0006).String(), (info.Mode() & 0007).String(), "other permission doesnt match")
	})
}
