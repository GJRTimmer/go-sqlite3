// Copyright (C) 2018 The Go-SQLite3 Authors.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build cgo
// +build !libsqlite3
// +build sqlite_see

package sqlite3

/*
#cgo CFLAGS: -DSQLITE_HAS_CODEC=1

#include <sqlite3-binding.h>
#include <stdlib.h>
*/
import "C"

//export sqlite3_activate_see
func sqlite3_activate_see(in *C.char) {
	// NO-OP
}

//export sqlite3_key
func sqlite3_key(db uintptr, pKey, nKey int) int {
	return C.SQLITE_OK
}

//export sqlite3_key_v2
func sqlite3_key_v2(db uintptr, zDb *C.char, pKey, nKey int) int {
	return C.SQLITE_OK
}

//export sqlite3_rekey
func sqlite3_rekey(db uintptr, pKey, nKey int) int {
	return C.SQLITE_OK
}

//export sqlite3_rekey_v2
func sqlite3_rekey_v2(db uintptr, zDb *C.char, pKey, nKey int) int {
	return C.SQLITE_OK
}
