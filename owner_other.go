// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT

//go:build !unix

package main

import "os"

// keepOwner does nothing where files have no Unix owner and group to keep.
func keepOwner(string, os.FileInfo) error {
	return nil
}
