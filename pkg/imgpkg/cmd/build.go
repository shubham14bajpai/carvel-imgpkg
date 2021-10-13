// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/cppforlife/go-cli-ui/ui"
	regname "github.com/google/go-containerregistry/pkg/name"
	regv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/k14s/imgpkg/pkg/imgpkg/bundle"
	ctlimg "github.com/k14s/imgpkg/pkg/imgpkg/image"
	ctlimgset "github.com/k14s/imgpkg/pkg/imgpkg/imageset"
	"github.com/k14s/imgpkg/pkg/imgpkg/imagetar"
	"github.com/k14s/imgpkg/pkg/imgpkg/plainimage"
	"github.com/k14s/imgpkg/pkg/imgpkg/registry"
	"github.com/k14s/imgpkg/pkg/imgpkg/util"
	"github.com/spf13/cobra"
)

type BuildOptions struct {
	ui ui.UI

	ImageFlags      ImageFlags
	BundleFlags     BundleFlags
	LockOutputFlags LockOutputFlags
	FileFlags       FileFlags
	RegistryFlags   RegistryFlags
}

func NewBuildOptions(ui ui.UI) *BuildOptions {
	return &BuildOptions{ui: ui}
}

func NewBuildCmd(o *BuildOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "build",
		Short:   "TODO",
		RunE:    func(_ *cobra.Command, _ []string) error { return o.Run() },
		Example: `TODO`,
	}
	o.ImageFlags.Set(cmd)
	o.BundleFlags.Set(cmd)
	o.FileFlags.Set(cmd)
	o.RegistryFlags.Set(cmd)
	return cmd
}

func (po *BuildOptions) Run() error {
	reg, err := registry.NewRegistry(po.RegistryFlags.AsRegistryOpts())
	if err != nil {
		return err
	}

	var imageURL string

	isBundle := po.BundleFlags.Bundle != ""
	isImage := po.ImageFlags.Image != ""

	switch {
	case isBundle && isImage:
		return fmt.Errorf("Expected only one of image or bundle")

	case !isBundle && !isImage:
		return fmt.Errorf("Expected either image or bundle")

	case isBundle:
		imageURL, err = po.buildBundle(reg)
		if err != nil {
			return err
		}

	case isImage:
		imageURL, err = po.buildImage(reg)
		if err != nil {
			return err
		}

	default:
		panic("Unreachable code")
	}

	po.ui.BeginLinef("Pushed '%s'", imageURL)

	return nil
}

func (po *BuildOptions) buildBundle(registry registry.Registry) (string, error) {
	prefixedLogger := util.NewUIPrefixedWriter("build | ", po.ui)
	levelLogger := util.NewUILevelLogger(util.LogWarn, prefixedLogger)

	buildImage, err := bundle.NewContents(po.FileFlags.Files, po.FileFlags.ExcludedFilePaths).Build(po.ui)
	if err != nil {
		return "", err
	}
	defer buildImage.Remove()

	builtBundleDigest, err := po.getDigest(po.BundleFlags.Bundle, buildImage)
	if err != nil {
		return "", err
	}

	tag, err := po.getTag(po.BundleFlags.Bundle)
	if err != nil {
		return "", err
	}

	plainImage := plainimage.NewFetchedPlainImageWithTag(builtBundleDigest, tag, buildImage.Image)
	rootBundle := bundle.NewBundleFromPlainImage(plainImage, registry)

	//TODO: thread via flag
	concurrency := 1
	_, imageRefs, err := rootBundle.AllImagesRefs(concurrency, levelLogger)
	if err != nil {
		return "", err
	}

	unprocessedImageRefs := ctlimgset.NewUnprocessedImageRefs()
	for _, img := range imageRefs.ImageRefs() {
		unprocessedImageRefs.Add(ctlimgset.UnprocessedImageRef{DigestRef: img.PrimaryLocation()})
	}

	unprocessedImageRefs.Add(ctlimgset.UnprocessedImageRef{
		DigestRef: rootBundle.DigestRef(),
		Tag:       rootBundle.Tag(),
		Labels: map[string]string{
			rootBundleLabelKey: "",
		}},
	)

	fetch, err := plainImage.Fetch()
	if err != nil {
		return "", err
	}

	localBundleReference, err := regname.ParseReference(builtBundleDigest)
	if err != nil {
		return "", err
	}

	registryWithLocalImage := RegistryWithLocalImage{registry, fetch, localBundleReference}

	// TODO: thread in via flags
	path := "/tmp/testbundle.tar"

	imageSet := ctlimgset.NewImageSet(concurrency, prefixedLogger)
	tarImageSet := ctlimgset.NewTarImageSet(imageSet, concurrency, prefixedLogger)

	_, err = tarImageSet.Export(unprocessedImageRefs, path, registryWithLocalImage,
		imagetar.NewImageLayerWriterCheck(false))
	if err != nil {
		return "", err
	}

	return "", nil
}

func (po *BuildOptions) buildImage(registry registry.Registry) (string, error) {
	prefixedLogger := util.NewUIPrefixedWriter("build | ", po.ui)

	if po.LockOutputFlags.LockFilePath != "" {
		return "", fmt.Errorf("Lock output is not compatible with image, use bundle for lock output")
	}

	contents := bundle.NewContents(po.FileFlags.Files, po.FileFlags.ExcludedFilePaths)
	isBundle, err := contents.PresentsAsBundle()
	if err != nil {
		return "", err
	}
	if isBundle {
		return "", fmt.Errorf("Images cannot be pushed with '.imgpkg' directories, consider using --bundle (-b) option")
	}

	//TODO: provide ui as the writer
	loggerWriter := os.Stdout
	tarImg := ctlimg.NewTarImage(po.FileFlags.Files, po.FileFlags.ExcludedFilePaths, loggerWriter)
	imageBuild, err := tarImg.AsFileImage(map[string]string{})
	if err != nil {
		return "", err
	}

	builtImageDigest, err := po.getDigest(po.ImageFlags.Image, imageBuild)
	if err != nil {
		return "", err
	}

	tag, err := po.getTag(po.ImageFlags.Image)
	if err != nil {
		return "", err
	}

	plainImage := plainimage.NewFetchedPlainImageWithTag(builtImageDigest, tag, imageBuild.Image)
	plainImageLocalBuild, err := plainImage.Fetch()
	if err != nil {
		return "", err
	}

	localImageRef, err := regname.ParseReference(builtImageDigest)
	if err != nil {
		return "", err
	}

	registryWithLocalImage := RegistryWithLocalImage{registry, plainImageLocalBuild, localImageRef}

	// TODO: thread in via flags
	path := "/tmp/testbundle.tar"
	//TODO: thread via flag
	concurrency := 1

	imageSet := ctlimgset.NewImageSet(concurrency, prefixedLogger)
	tarImageSet := ctlimgset.NewTarImageSet(imageSet, concurrency, prefixedLogger)

	unprocessedImageRefs := ctlimgset.NewUnprocessedImageRefs()
	unprocessedImageRefs.Add(ctlimgset.UnprocessedImageRef{
		DigestRef: plainImage.DigestRef(),
		Tag:       plainImage.Tag(),
	},
	)

	_, err = tarImageSet.Export(unprocessedImageRefs, path, registryWithLocalImage,
		imagetar.NewImageLayerWriterCheck(false))
	if err != nil {
		return "", err
	}

	return "", nil
}

func (po *BuildOptions) getDigest(imageRef string, buildImage *ctlimg.FileImage) (string, error) {
	digest, err := buildImage.Digest()
	if err != nil {
		return "", err
	}

	parseReference, err := regname.ParseReference(imageRef)
	if err != nil {
		return "", err
	}

	newDigest, err := regname.NewDigest(parseReference.Context().RepositoryStr() + "@" + digest.String())
	if err != nil {
		return "", err
	}

	return newDigest.Name(), nil
}

func (po *BuildOptions) getTag(imageRef string) (string, error) {
	uploadRef, err := regname.NewTag(imageRef, regname.WeakValidation)
	if err != nil {
		return "", fmt.Errorf("Parsing '%s': %s", imageRef, err)
	}
	return uploadRef.TagStr(), nil
}

type RegistryWithLocalImage struct {
	ctlimgset.ImagesReaderWriter

	localImage regv1.Image
	reference  regname.Reference
}

func (r RegistryWithLocalImage) Get(reference regname.Reference) (*remote.Descriptor, error) {
	if reference.Identifier() == r.reference.Identifier() {
		manifest, err := r.localImage.RawManifest()
		if err != nil {
			return nil, err
		}
		return &remote.Descriptor{
			Manifest: manifest,
		}, nil
	}
	return r.ImagesReaderWriter.Get(reference)
}

func (r RegistryWithLocalImage) Digest(reference regname.Reference) (regv1.Hash, error) {
	if reference.Identifier() == r.reference.Identifier() {
		return r.localImage.Digest()
	}

	return r.ImagesReaderWriter.Digest(reference)
}

func (r RegistryWithLocalImage) Image(reference regname.Reference) (regv1.Image, error) {
	if reference.Identifier() == r.reference.Identifier() {
		return r.localImage, nil
	}

	return r.ImagesReaderWriter.Image(reference)
}
