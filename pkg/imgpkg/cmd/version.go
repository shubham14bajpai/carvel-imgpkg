// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"time"

	"github.com/cppforlife/go-cli-ui/ui"
	regregistry "github.com/google/go-containerregistry/pkg/registry"
	"github.com/spf13/cobra"
)

var Version = "develop"

type VersionOptions struct {
	ui ui.UI
}

func NewVersionOptions(ui ui.UI) *VersionOptions {
	return &VersionOptions{ui}
}

func NewVersionCmd(o *VersionOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print client version",
		RunE:  func(_ *cobra.Command, _ []string) error { return o.Run() },
	}
	return cmd
}

func (o *VersionOptions) Run() error {
	o.ui.PrintBlock([]byte(fmt.Sprintf("imgpkg version %s\n", Version)))

	server := httptest.NewTLSServer(regregistry.New(regregistry.Logger(log.New(io.Discard, "", 0))))

	println(server.URL)
	time.Sleep(30 * time.Minute)

	return nil
}
