// Copyright (C) 2018 The Go-SQLite3 Authors.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build libsqlite3
// +build sqlite_see

package sqlite3

/*
#cgo CFLAGS: -DSQLITE_HAS_CODEC=1
*/
import "C"
