// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"io"
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

// TestWriteOutputWritesThroughFIFO covers an output that is a named pipe.
// os.WriteFile opened it and wrote the digest to whoever read the pipe;
// renaming a temp file over it removed the pipe and still reported success.
func TestWriteOutputWritesThroughFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.md")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	// A non-blocking reader lets the write open the pipe without waiting, and
	// reads EOF at once if nothing ever opens the pipe for writing.
	reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	if err := writeOutput(path, []byte("digest\n")); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if string(got) != "digest\n" {
		t.Errorf("pipe reader got %q, want %q", got, "digest\n")
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat output: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("output mode = %v, want the named pipe kept", info.Mode())
	}
}

// TestWriteOutputReplacesRegularFileAtomically pins that writeOutput sends a
// regular output through writeFileAtomic. A reader that opened the old digest
// keeps reading it after an atomic replace, where an in-place write such as
// os.WriteFile's would hand that reader the new bytes, or a truncated mix.
// Unix-only because Windows refuses to rename over a file that is open.
func TestWriteOutputReplacesRegularFileAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.md")
	if err := os.WriteFile(path, []byte("old digest\n"), 0o644); err != nil {
		t.Fatalf("seed output: %v", err)
	}

	reader, err := os.Open(path)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	if err := writeOutput(path, []byte("new digest\n")); err != nil {
		t.Fatalf("writeOutput: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read old output: %v", err)
	}
	if string(got) != "old digest\n" {
		t.Errorf("reader of the old output got %q, want %q: the output was rewritten in place", got, "old digest\n")
	}

	if got, err := os.ReadFile(path); err != nil || string(got) != "new digest\n" {
		t.Errorf("output = %q, %v; want %q", got, err, "new digest\n")
	}
}
