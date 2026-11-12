// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteFileAtomicNewOutputHonorsUmask covers a first run, where no output
// exists yet. os.WriteFile created it as 0644 narrowed by the umask, and a
// forced 0644 on the replacement made it readable to other users even under
// umask 077. This file is Unix-only because syscall.Umask has no Windows
// counterpart, and the test is not parallel because the umask is per process.
func TestWriteFileAtomicNewOutputHonorsUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	path := filepath.Join(t.TempDir(), "output.md")
	if err := writeFileAtomic(path, []byte("first digest\n")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("output mode = %o, want 600 under umask 077", mode)
	}
}
