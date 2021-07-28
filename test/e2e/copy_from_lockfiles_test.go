// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	regname "github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	regremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/k14s/imgpkg/pkg/imgpkg/lockconfig"
	"github.com/k14s/imgpkg/test/helpers"
	"github.com/stretchr/testify/require"
)

func TestCopyFromBundleLock(t *testing.T) {
	env := helpers.BuildEnv(t)
	imgpkg := helpers.Imgpkg{T: t, ImgpkgPath: env.ImgpkgPath}
	logger := helpers.Logger{}
	defer env.Cleanup()

	logger.Section("create random image for tests", func() {
		env.ImageFactory.PushSimpleAppImageWithRandomFile(imgpkg, env.Image)
	})

	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprintf("When Copying bundle it generates image with locations: %d", i), func(t *testing.T) {
			env := helpers.BuildEnv(t)
			imgpkg := helpers.Imgpkg{T: t, ImgpkgPath: env.ImgpkgPath}
			defer env.Cleanup()

			imgRef, err := regname.ParseReference(env.Image)
			require.NoError(t, err)

			var img1DigestRef, img2DigestRef, img1Digest, img2Digest string

			logger.Section("create simple images", func() {
				img1DigestRef = imgRef.Context().Name() + "-img1"
				img1Digest = env.ImageFactory.PushSimpleAppImageWithRandomFile(imgpkg, img1DigestRef)
				img1DigestRef = img1DigestRef + img1Digest

				img2DigestRef = imgRef.Context().Name() + "-img2"
				img2Digest = env.ImageFactory.PushSimpleAppImageWithRandomFile(imgpkg, img2DigestRef)
				img2DigestRef = img2DigestRef + img2Digest

			})

			simpleBundle := imgRef.Context().Name() + "-simple-bundle"
			simpleBundleDigest := ""
			logger.Section("create simple bundle", func() {
				imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
  annotations:
    a: b
- image: %s
  annotations:
    a: c
`, img1DigestRef, img1DigestRef)

				bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, imageLockYAML)
				out := imgpkg.Run([]string{"push", "--tty", "-b", simpleBundle, "-f", bundleDir})
				simpleBundleDigest = fmt.Sprintf("@%s", helpers.ExtractDigest(t, out))
			})

			nestedBundle := imgRef.Context().Name() + "-bundle-nested"
			nestedBundleDigest := ""
			logger.Section("create nested bundle that contains images and the simple bundle", func() {
				imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
  annotations:
    a: b
- image: %s
  annotations:
    a: c
- image: %s
  annotations:
    a: d
- image: %s
  annotations:
    a: e
`, img1DigestRef, img2DigestRef, img1DigestRef, simpleBundle+simpleBundleDigest)

				bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, imageLockYAML)
				out := imgpkg.Run([]string{"push", "--tty", "-b", nestedBundle, "-f", bundleDir})
				nestedBundleDigest = fmt.Sprintf("@%s", helpers.ExtractDigest(t, out))
			})

			outerBundle := imgRef.Context().Name() + "-bundle-outer"
			outerBundleDigest := ""
			bundleTag := fmt.Sprintf(":%d", time.Now().UnixNano())
			bundleToCopy := fmt.Sprintf("%s%s", outerBundle, bundleTag)
			var lockFile string

			logger.Section("create outer bundle with image, simple bundle and nested bundle", func() {
				imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
  annotations:
    a: a
- image: %s
  annotations:
    a: b
- image: %s
  annotations:
    a: c
- image: %s
  annotations:
    a: d
- image: %s
  annotations:
    a: e
`, nestedBundle+nestedBundleDigest, nestedBundle+nestedBundleDigest, img1DigestRef, simpleBundle+simpleBundleDigest, simpleBundle+simpleBundleDigest)

				bundleDir := env.BundleFactory.CreateBundleDir(helpers.BundleYAML, imageLockYAML)
				lockFile = filepath.Join(bundleDir, "bundle.lock.yml")
				out := imgpkg.Run([]string{"push", "--tty", "-b", bundleToCopy, "-f", bundleDir, "--lock-output", lockFile})
				outerBundleDigest = fmt.Sprintf("@%s", helpers.ExtractDigest(t, out))
			})

			var lastOuterBundleDigest, lastNestedBundleDigest, lastSimpleBundleDigest string
			for i := 0; i < 10; i++ {
				println(fmt.Sprintf("iteration ==> %d", i))
				logger.Section("copy bundle to repository", func() {
					out := imgpkg.Run([]string{"copy",
						"--lock", lockFile,
						"--to-repo", env.RelocationRepo},
					)
					fmt.Println(out)
				})

				logger.Section("download the locations file for outer bundle and check it", func() {
					hash, err := v1.NewHash(outerBundleDigest[1:])
					require.NoError(t, err)
					locationImg := fmt.Sprintf("%s:%s-%s.image-locations.imgpkg", env.RelocationRepo, hash.Algorithm, hash.Hex)
					imageReg, err := regname.ParseReference(locationImg, regname.WeakValidation)
					require.NoError(t, err)
					head, err := regremote.Head(imageReg, regremote.WithAuthFromKeychain(authn.DefaultKeychain))
					require.NoError(t, err)

					if lastOuterBundleDigest == "" {
						println("should only enter once")
						lastOuterBundleDigest = head.Digest.String()
					}

					if lastOuterBundleDigest != head.Digest.String() {
						t.FailNow()
					}
				})

				logger.Section("download the locations file for nested bundle and check it", func() {
					hash, err := v1.NewHash(nestedBundleDigest[1:])
					require.NoError(t, err)
					locationImg := fmt.Sprintf("%s:%s-%s.image-locations.imgpkg", env.RelocationRepo, hash.Algorithm, hash.Hex)
					imageReg, err := regname.ParseReference(locationImg, regname.WeakValidation)
					require.NoError(t, err)
					head, err := regremote.Head(imageReg, regremote.WithAuthFromKeychain(authn.DefaultKeychain))
					require.NoError(t, err)

					if lastNestedBundleDigest == "" {
						println("should only enter once")
						lastNestedBundleDigest = head.Digest.String()
					}

					if lastNestedBundleDigest != head.Digest.String() {
						t.FailNow()
					}

				})

				logger.Section("download the locations file for simple bundle and check it", func() {
					hash, err := v1.NewHash(simpleBundleDigest[1:])
					require.NoError(t, err)
					locationImg := fmt.Sprintf("%s:%s-%s.image-locations.imgpkg", env.RelocationRepo, hash.Algorithm, hash.Hex)
					imageReg, err := regname.ParseReference(locationImg, regname.WeakValidation)
					require.NoError(t, err)
					head, err := regremote.Head(imageReg, regremote.WithAuthFromKeychain(authn.DefaultKeychain))
					require.NoError(t, err)

					if lastSimpleBundleDigest == "" {
						println("should only enter once")
						lastSimpleBundleDigest = head.Digest.String()
					}

					if lastSimpleBundleDigest != head.Digest.String() {
						t.FailNow()
					}

				})
			}
		})
	}
}

func TestCopyFromImageLock(t *testing.T) {
	env := helpers.BuildEnv(t)
	imgpkg := helpers.Imgpkg{T: t, L: helpers.Logger{}, ImgpkgPath: env.ImgpkgPath}
	logger := helpers.Logger{}
	defer env.Cleanup()

	randomImageDigest := ""
	randomImageDigestRef := ""
	logger.Section("create random image for tests", func() {
		randomImageDigest = env.ImageFactory.PushSimpleAppImageWithRandomFile(imgpkg, env.Image)
		randomImageDigestRef = env.Image + randomImageDigest
	})

	t.Run("when copying to repo, it is successful and generates an ImageLock file", func(t *testing.T) {
		env.UpdateT(t)
		imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
  annotations:
    some-annotation: some-value
`, randomImageDigestRef)

		testDir := env.Assets.CreateTempFolder("copy-image-to-repo-with-lock-file")
		lockFile := filepath.Join(testDir, "images.lock.yml")
		err := ioutil.WriteFile(lockFile, []byte(imageLockYAML), 0700)
		require.NoError(t, err)

		logger.Section("copy from lock file", func() {
			lockOutputPath := filepath.Join(testDir, "image-relocate-lock.yml")
			imgpkg.Run([]string{"copy", "--lock", lockFile, "--to-repo", env.RelocationRepo, "--lock-output", lockOutputPath})

			imageRefs := []lockconfig.ImageRef{{
				Image:       fmt.Sprintf("%s%s", env.RelocationRepo, randomImageDigest),
				Annotations: map[string]string{"some-annotation": "some-value"},
			}}
			env.Assert.AssertImagesLock(lockOutputPath, imageRefs)

			refs := []string{env.RelocationRepo + randomImageDigest}
			require.NoError(t, env.Assert.ValidateImagesPresenceInRegistry(refs))
		})
	})

	t.Run("when Copying images to Tar file and after importing to a new Repo, it keeps the tags and generates a ImageLock file", func(t *testing.T) {
		env.UpdateT(t)
		imageLockYAML := fmt.Sprintf(`---
apiVersion: imgpkg.carvel.dev/v1alpha1
kind: ImagesLock
images:
- image: %s
`, randomImageDigestRef)

		testDir := env.Assets.CreateTempFolder("copy--image-lock-via-tar-keep-tag")
		lockFile := filepath.Join(testDir, "images.lock.yml")

		err := ioutil.WriteFile(lockFile, []byte(imageLockYAML), 0700)
		require.NoError(t, err)

		tarFilePath := filepath.Join(testDir, "image.tar")
		logger.Section("copy image to tar file", func() {
			imgpkg.Run([]string{"copy", "--lock", lockFile, "--to-tar", tarFilePath})

			env.Assert.ImagesDigestIsOnTar(tarFilePath, randomImageDigestRef)
		})

		lockOutputPath := filepath.Join(testDir, "relocate-from-tar-lock.yml")
		logger.Section("import tar to new repository", func() {
			imgpkg.Run([]string{"copy", "--tar", tarFilePath, "--to-repo", env.RelocationRepo, "--lock-output", lockOutputPath})

			expectedRef := fmt.Sprintf("%s%s", env.RelocationRepo, randomImageDigest)
			env.Assert.AssertImagesLock(lockOutputPath, []lockconfig.ImageRef{{Image: expectedRef}})

			refs := []string{env.RelocationRepo + randomImageDigest}
			require.NoError(t, env.Assert.ValidateImagesPresenceInRegistry(refs))
		})
	})
}
