// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	// aws credential provider via init()
	_ "github.com/vdemeester/k8s-pkg-credentialprovider/aws"

	// azure credential provider via init()
	_ "github.com/vdemeester/k8s-pkg-credentialprovider/azure"

	// gcp credential provider via init()
	_ "github.com/vdemeester/k8s-pkg-credentialprovider/gcp"

	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
)

func init() {
	klog.SetLogger(klogr.New())
}
