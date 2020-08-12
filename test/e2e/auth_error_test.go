// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"strings"
	"testing"
)

func TestAuthErr(t *testing.T) {
	t.Skip("skipping test due to regression in error message returned from ggcr")

	env := BuildEnv(t)
	imgpkg := Imgpkg{t, Logger{}}

	var stderrBs bytes.Buffer

	_, err := imgpkg.RunWithOpts([]string{
		"pull", "-i", env.Image, "-o", "/tmp/unused",
		"--registry-username", "incorrect-user",
		"--registry-password", "incorrect-password",
	}, RunOpts{AllowError: true, StderrWriter: &stderrBs})

	errOut := stderrBs.String()

	if err == nil {
		t.Fatalf("Expected auth error")
	}
	if !strings.Contains(errOut, "UNAUTHORIZED: incorrect username or password") {
		t.Fatalf("Expected auth error explanation in output '%s'", errOut)
	}
}
