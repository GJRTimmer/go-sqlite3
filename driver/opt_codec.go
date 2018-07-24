// Copyright (C) 2018 The Go-SQLite3 Authors.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build cgo
// +build !libsqlite3
// +build sqlite_codec sqlite_encrypt sqlite_see

package sqlite3

/*
#cgo CFLAGS: -DSQLITE_HAS_CODEC=1
#cgo LDFLAGS: -lm

#include <sqlite3-binding.h>
#include <stdlib.h>
*/
import "C"
