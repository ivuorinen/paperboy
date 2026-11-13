// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"os"
	"syscall"
)

// keepOwner gives the file at name the owner and group recorded in info, which
// os.Stat on Unix always backs with a *syscall.Stat_t. The renamed replacement
// is a new file that would otherwise belong to the process's user and group,
// so a group that could read the old digest would lose access to the new one.
// Setting another user as owner, or a group the process is not in, needs
// root. That error is returned, so the old output stays rather than being
// replaced with different ownership.
func keepOwner(name string, info os.FileInfo) error {
	st := info.Sys().(*syscall.Stat_t)

	return os.Chown(name, int(st.Uid), int(st.Gid))
}
