// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package maacontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/adb"

	"github.com/kvarenzn/ssm/core/log"
)

// BuyMusicConfig holds the minimal parameters needed to run the buy-music loop.
type BuyMusicConfig struct {
	AdbPath     string // path to adb binary; empty = search PATH
	AdbSerial   string // device serial; empty = auto-detect by MAA
	ResourceDir string // root resource dir; defaults to "./maacontrol/resource"
	MaaLibDir   string // directory containing MAA native libs; empty = PATH/CWD
}

// RunBuyMusicLoop connects to the device via MAA's ADB controller and runs
// the buy_music_loop pipeline task until ctx is cancelled.
// It returns an error if the setup fails; pipeline errors during the loop are
// logged but do not terminate the loop (it keeps retrying).
func RunBuyMusicLoop(ctx context.Context, cfg BuyMusicConfig) error {
	if err := ensureMaaInit(cfg.MaaLibDir); err != nil {
		return fmt.Errorf("maa init: %w", err)
	}

	resourceDir := cfg.ResourceDir
	if resourceDir == "" {
		resourceDir = "./maacontrol/resource"
	}

	// Build a minimal pipeline dir containing only buy_music.json
	tmpDir, err := os.MkdirTemp("", "ssm-buym-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Read buy_music.json from the resource pipeline directory
	srcJSON := filepath.Join(resourceDir, "pipeline", "buy_music.json")
	pipelineData, err := os.ReadFile(srcJSON)
	if err != nil {
		return fmt.Errorf("read buy_music.json: %w", err)
	}
	// Validate JSON
	if !json.Valid(pipelineData) {
		return fmt.Errorf("buy_music.json is not valid JSON")
	}

	pipelineDir := filepath.Join(tmpDir, "pipeline")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		return fmt.Errorf("mkdir pipeline: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pipelineDir, "buy_music.json"), pipelineData, 0o644); err != nil {
		return fmt.Errorf("write buy_music.json: %w", err)
	}

	adbPath := cfg.AdbPath
	if adbPath == "" {
		adbPath = "adb"
	}

	log.Infof("[BuyMusic] connecting via MAA ADB serial=%q", cfg.AdbSerial)
	ctrl, err := maa.NewAdbController(
		adbPath,
		cfg.AdbSerial,
		adb.ScreencapDefault,
		adb.InputAdbShell,
		"{}", "",
	)
	if err != nil {
		return fmt.Errorf("maa adb controller: %w", err)
	}
	defer ctrl.Destroy()

	if !ctrl.PostConnect().Wait().Success() {
		return fmt.Errorf("maa: connect to %q failed", cfg.AdbSerial)
	}

	res, err := maa.NewResource()
	if err != nil {
		return fmt.Errorf("maa resource: %w", err)
	}
	defer res.Destroy()

	if !res.PostBundle(tmpDir).Wait().Success() {
		return fmt.Errorf("maa resource bundle load from %q failed", tmpDir)
	}

	tasker, err := maa.NewTasker()
	if err != nil {
		return fmt.Errorf("maa tasker: %w", err)
	}
	defer tasker.Destroy()

	if err := tasker.BindController(ctrl); err != nil {
		return fmt.Errorf("maa bind controller: %w", err)
	}
	if err := tasker.BindResource(res); err != nil {
		return fmt.Errorf("maa bind resource: %w", err)
	}

	log.Infof("[BuyMusic] starting buy_music_loop")
	job := tasker.PostTask("buy_music_loop", "{}")

	done := make(chan bool, 1)
	go func() {
		done <- job.Wait().Success()
	}()

	select {
	case <-ctx.Done():
		tasker.PostStop()
		<-done
		log.Infof("[BuyMusic] stopped")
		return nil
	case ok := <-done:
		if !ok {
			return fmt.Errorf("buy_music_loop task failed")
		}
		return nil
	}
}
