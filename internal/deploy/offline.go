package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Признаки офлайн-режима.
const (
	// EnvOffline — Z2R_OFFLINE=1 (так же называется флаг в shell-версии;
	// выставляется офлайн-лаунчером после проверки бандла).
	EnvOffline = "Z2R_OFFLINE"
	// EnvOfflineAlias — синоним ZATOR_OFFLINE=1.
	EnvOfflineAlias = "ZATOR_OFFLINE"
	// EnvBundleDir — каталог офлайн-бандла (vendor/ + zator-*.tar.gz).
	EnvBundleDir = "ZATOR_BUNDLE_DIR"
	// BundleVendorDir — подкаталог бандла с зависимостями.
	BundleVendorDir = "vendor"
	// BundleTarballGlob — шаблон архива zator внутри бандла.
	BundleTarballGlob = "zator-*.tar.gz"
)

// Bundle — офлайн-бандл: каталог с vendor/ и архивом zator-<variant>.tar.gz.
type Bundle struct {
	Dir           string
	VendorDir     string
	AppArchive    string
	ConfigArchive string
	// Tarball — архив zator; при нескольких предпочитается zator-full.tar.gz.
	Tarball string
}

// FindBundle ищет офлайн-бандл в каталоге dir.
func FindBundle(dir string) (Bundle, bool) {
	if dir == "" {
		return Bundle{}, false
	}
	vendor := filepath.Join(dir, BundleVendorDir)
	if st, err := os.Stat(vendor); err != nil || !st.IsDir() {
		return Bundle{}, false
	}
	matches, err := filepath.Glob(filepath.Join(dir, BundleTarballGlob))
	if err != nil || len(matches) == 0 {
		return Bundle{}, false
	}
	var files []string
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() {
			files = append(files, m)
		}
	}
	if len(files) == 0 {
		return Bundle{}, false
	}
	sort.Strings(files)
	tar := files[len(files)-1]
	for _, f := range files {
		if filepath.Base(f) == "zator-full.tar.gz" {
			tar = f
		}
	}
	b := Bundle{Dir: dir, VendorDir: vendor, Tarball: tar}
	for _, f := range files {
		switch filepath.Base(f) {
		case "zator-app.tar.gz":
			b.AppArchive = f
		case "zator-config.tar.gz":
			b.ConfigArchive = f
		}
	}
	return b, true
}

// ApplyBundleMode installs only the selected resource archives and binary.
func (u *Updater) ApplyBundleMode(ctx context.Context, b Bundle, mode string) (*ApplyResult, error) {
	if mode == "" {
		mode = ModeFull
	}
	if mode != ModeApp && mode != ModeConfig && mode != ModeFull {
		return nil, fmt.Errorf("%w: mode %q", ErrUnknownVariant, mode)
	}
	if b.AppArchive == "" && b.ConfigArchive == "" {
		if mode == ModeFull && b.Tarball != "" {
			res, err := u.ApplyBundle(b, "")
			if err != nil {
				return nil, err
			}
			bin, err := u.ApplyBundleBinary(ctx, b)
			if err != nil {
				return nil, errors.Join(err, res.finishTransaction(false))
			}
			if res != nil {
				res.Installed += bin.Installed
			}
			return res, nil
		}
		return nil, fmt.Errorf("deploy: offline bundle does not contain split resource archives")
	}
	var specs []archiveSpec
	if mode != ModeConfig {
		if b.AppArchive == "" {
			return nil, fmt.Errorf("deploy: offline archive %s is missing", ModeApp)
		}
		specs = append(specs, archiveSpec{file: b.AppArchive, tracking: "offline", component: ModeApp})
	}
	if mode != ModeApp {
		if b.ConfigArchive == "" {
			return nil, fmt.Errorf("deploy: offline archive %s is missing", ModeConfig)
		}
		specs = append(specs, archiveSpec{file: b.ConfigArchive, tracking: "offline", component: ModeConfig})
	}
	tx, err := u.applyArchives(specs)
	if err != nil {
		return tx.result, err
	}
	if mode != ModeConfig {
		bin, err := u.ApplyBundleBinary(ctx, b)
		if err != nil {
			return nil, tx.rollback(err)
		}
		if tx.result != nil {
			tx.result.Installed += bin.Installed
		}
	}
	if err := tx.finish(true); err != nil {
		return tx.result, err
	}
	return tx.result, nil
}

// ApplyBundleResources installs resources from an offline bundle without replacing
// the current binary. This is the resource phase of a fresh install.
func (u *Updater) ApplyBundleResources(b Bundle, mode string) (*ApplyResult, error) {
	files := map[string]string{}
	if b.AppArchive != "" {
		files[VariantApp] = b.AppArchive
	}
	if b.ConfigArchive != "" {
		files[VariantConfig] = b.ConfigArchive
	}
	if len(files) == 0 && mode == ModeFull && b.Tarball != "" {
		res, err := u.ApplyBundle(b, "")
		return res, err
	}
	return u.applyResourceArchives(mode, files, nil, "offline")
}

// OfflineStatus — результат определения офлайн-режима.
type OfflineStatus struct {
	Offline bool
	// Reason — "env" (Z2R_OFFLINE=1), "bundle" (найден бандл) или "".
	Reason string
	Bundle *Bundle
}

// DetectOffline определяет офлайн-режим: по переменной окружения (getenv, nil
// — os.Getenv) и по наличию бандла в каталоге bundleDir ("" — не искать).
func DetectOffline(getenv func(string) string, bundleDir string) OfflineStatus {
	if getenv == nil {
		getenv = os.Getenv
	}
	var st OfflineStatus
	if b, ok := FindBundle(bundleDir); ok {
		st.Bundle = &b
	}
	for _, k := range []string{EnvOffline, EnvOfflineAlias} {
		if strings.TrimSpace(getenv(k)) == "1" {
			st.Offline, st.Reason = true, "env"
			return st
		}
	}
	if st.Bundle != nil {
		st.Offline, st.Reason = true, "bundle"
	}
	return st
}

// ApplyBundle применяет архив офлайн-бандла без обращения к сети. Версия
// записывается с TRACKING = "offline-<tag>" (tag пуст — "offline").
func (u *Updater) ApplyBundle(b Bundle, tag string) (*ApplyResult, error) {
	tracking := "offline"
	if tag = strings.TrimSpace(tag); tag != "" {
		tracking = "offline-" + tag
	}
	// Старые bundles могли содержать x86_64 bin/zator. Runtime-бинарник
	// выбирается отдельно из vendor по архитектуре устройства.
	bundleUpdater := *u
	priorRemap := bundleUpdater.Remap
	bundleUpdater.Remap = func(rel string) string {
		if filepath.ToSlash(filepath.Clean(rel)) == "bin/zator" {
			return ""
		}
		if priorRemap != nil {
			return priorRemap(rel)
		}
		return rel
	}
	return bundleUpdater.ApplyArchive(b.Tarball, ApplyOptions{Tracking: tracking})
}

// ResolveOfflineBundle ищет бандл в ZATOR_BUNDLE_DIR, cwd и рядом с бинарником.
// Offline=true также при Z2R_OFFLINE=1 (даже без бандла).
func ResolveOfflineBundle(getenv func(string) string) OfflineStatus {
	if getenv == nil {
		getenv = os.Getenv
	}
	dirs := make([]string, 0, 4)
	if v := strings.TrimSpace(getenv(EnvBundleDir)); v != "" {
		dirs = append(dirs, v)
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		dirs = append(dirs, cwd)
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		dirs = append(dirs, filepath.Dir(exe))
	}
	var found *Bundle
	for _, d := range dirs {
		if b, ok := FindBundle(d); ok {
			bb := b
			found = &bb
			break
		}
	}
	st := DetectOffline(getenv, "")
	if found != nil {
		st.Bundle = found
		if !st.Offline {
			st.Offline, st.Reason = true, "bundle"
		}
	}
	return st
}
