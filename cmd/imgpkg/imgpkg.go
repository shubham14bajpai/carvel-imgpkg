// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"time"

	"github.com/cppforlife/go-cli-ui/ui"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/k14s/imgpkg/pkg/imgpkg/cmd"
)

func main() {
	cpuFile := cpuProfiling()
	defer cpuFile.Close() // error handling omitted for example
	defer pprof.StopCPUProfile()

	memFile := memProfiling()
	defer memFile.Close() // error handling omitted for example

	f, err := os.Create("/tmp/profiling/trace.out")
	if err != nil {
		log.Fatalf("failed to create trace output file: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Fatalf("failed to close trace file: %v", err)
		}
	}()

	if err := trace.Start(f); err != nil {
		log.Fatalf("failed to start trace: %v", err)
	}
	defer trace.Stop()

	rand.Seed(time.Now().UTC().UnixNano())

	log.SetOutput(os.Stderr)

	logs.Warn.SetOutput(os.Stderr)
	logs.Progress.SetOutput(os.Stderr)

	confUI := ui.NewConfUI(ui.NewNoopLogger())
	defer confUI.Flush()

	command := cmd.NewDefaultImgpkgCmd(confUI)

	err = command.Execute()
	if err != nil {
		confUI.ErrorLinef("Error: %v", err)
		os.Exit(1)
	}

	confUI.PrintLinef("Succeeded")
}

func memProfiling() *os.File {
	f, err := os.Create("/tmp/profiling/mem-profiling")
	if err != nil {
		log.Fatal("could not create memory profile: ", err)
	}
	runtime.GC() // get up-to-date statistics
	if err := pprof.WriteHeapProfile(f); err != nil {
		log.Fatal("could not write memory profile: ", err)
	}
	return f
}

func cpuProfiling() *os.File {
	f, err := os.Create("/tmp/profiling/cpu-profiling")
	if err != nil {
		log.Fatal("could not create CPU profile: ", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		log.Fatal("could not start CPU profile: ", err)
	}
	return f
}
