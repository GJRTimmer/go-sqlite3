// Copyright (C) 2018 The Go-SQLite3 Authors.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Modified from: https://github.com/mxk/go-sqlite

// +build cgo
// +build !libsqlite3
// +build sqlite_codec sqlite_encrypt sqlite_see

package sqlite3

/*
#include <sqlite3-binding.h>
#include <stdlib.h>
*/
import "C"
import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"hash"
	"io"
	"strings"
	"sync"
	"unsafe"
)

// Errors returned by codec implementations.
var (
	codecErr = &Error{Code: C.SQLITE_ERROR, err: "unspecified codec error"}
	prngErr  = &Error{Code: C.SQLITE_ERROR, err: "csprng not available"}
	keyErr   = &Error{Code: C.SQLITE_MISUSE, err: "invalid codec key format"}
)

// CodecFunc is a codec initialization function registered for a specific key
// prefix via RegisterCodec. It is called when a key with a matching prefix is
// specified for an attached database. It returns the Codec implementation that
// should be used to encode and decode all database and journal pages. Returning
// (nil, nil) disables the codec.
type CodecFunc func(ctx *CodecCtx, key []byte) (Codec, *Error)

// CodecCtx describes the database to which a codec is being attached.
type CodecCtx struct {
	Path     string // Full path to the database file
	Name     string // Database name as it is known to SQLite (e.g. "main")
	PageSize int    // Current page size in bytes
	Reserve  int    // Current number of bytes reserved in each page
	Fixed    bool   // True if the PageSize and Reserve values cannot be changed
}

// newCodecCtx converts the C CodecCtx struct into its Go representation.
func newCodecCtx(ctx *C.CodecCtx) *CodecCtx {
	return &CodecCtx{
		Path:     C.GoString(ctx.zPath),
		Name:     C.GoString(ctx.zName),
		PageSize: int(ctx.nBuf),
		Reserve:  int(ctx.nRes),
		Fixed:    ctx.fixed != 0,
	}
}

// Codec is the interface used to encode/decode database and journal pages as
// they are written to and read from the disk.
//
// The op value passed to Encode and Decode methods identifies the operation
// being performed. It is undocumented and changed meanings over time since the
// codec API was first introduced in 2004. It is believed to be a bitmask of the
// following values:
//
// 	1 = journal page, not set for WAL, always set when decoding
// 	2 = disk I/O, always set
// 	4 = encode
//
// In the current implementation, op is always 3 when decoding, 6 when encoding
// for the database file or the WAL, and 7 when encoding for the journal. Search
// lib/sqlite3.c for "CODEC1" and "CODEC2" for more information.
type Codec interface {
	// Reserve returns the number of bytes that should be reserved for the codec
	// at the end of each page. The upper limit is 255 (32 if the page size is
	// 512). Returning -1 leaves the current value unchanged.
	Reserve() int

	// Resize is called when the codec is first attached to the pager and for
	// all subsequent page size changes. It can be used to allocate the encode
	// buffer.
	Resize(pageSize, reserve int)

	// Encode returns an encoded copy of a page. The data outside of the reserve
	// area in the original page must not be modified. The codec can either copy
	// this data into a buffer for encoding or return the original page without
	// making any changes. Bytes 16 through 23 of page 1 cannot be encoded. Any
	// non-nil error will be interpreted by SQLite as a NOMEM condition. This is
	// a limitation of underlying C API.
	Encode(page []byte, pageNum uint32, op int) ([]byte, *Error)

	// Decode decodes the page in-place, but it may use the encode buffer as
	// scratch space. Bytes 16 through 23 of page 1 must be left at their
	// original values. Any non-nil error will be interpreted by SQLite as a
	// NOMEM condition. This is a limitation of underlying C API.
	Decode(page []byte, pageNum uint32, op int) *Error

	// Key returns the original key that was used to initialize the codec. Some
	// implementations may be better off returning nil or a fake value. Search
	// lib/sqlite3.c for "sqlite3CodecGetKey" to see how the key is used.
	Key() []byte

	// Free releases codec resources when the pager is destroyed or when the
	// codec attachment fails.
	Free()
}

// Codec registry and state reference maps.
var (
	codecReg   map[string]CodecFunc
	codecState map[*codec]struct{}
	codecMu    sync.Mutex
)

// RegisterCodec adds a new codec to the internal registry. Function f will be
// called when a key in the format "<name>:<...>" is provided to an attached
// database.
func RegisterCodec(name string, f CodecFunc) {
	codecMu.Lock()
	defer codecMu.Unlock()
	if f == nil {
		delete(codecReg, name)
		return
	}
	if codecReg == nil {
		codecReg = make(map[string]CodecFunc, 8)
	}
	codecReg[name] = f
}

// getCodec returns the CodecFunc for the given key.
func getCodec(key []byte) CodecFunc {
	i := bytes.IndexByte(key, ':')
	if i == -1 {
		i = len(key)
	}
	codecMu.Lock()
	defer codecMu.Unlock()
	if codecReg == nil {
		return nil
	}
	return codecReg[bstr(key[:i])]
}

// codec is a wrapper around the actual Codec interface. It keeps track of the
// current page size in order to convert page pointers into byte slices.
type codec struct {
	Codec
	pageSize C.int
}

//export go_codec_init
func go_codec_init(ctx *C.CodecCtx, pCodec *unsafe.Pointer, pzErrMsg **C.char) C.int {
	cf := getCodec(goBytes(ctx.pKey, ctx.nKey))
	if cf == nil {
		*pzErrMsg = C.CString("codec not found")
		return C.SQLITE_ERROR
	}
	ci, err := cf(newCodecCtx(ctx), C.GoBytes(ctx.pKey, ctx.nKey))
	if err != nil && err.Code != C.SQLITE_OK {
		if ci != nil {
			ci.Free()
		}
		if err.err != "" {
			*pzErrMsg = C.CString(err.err)
		}
		return C.int(err.Code)
	}
	if ci != nil {
		cs := &codec{ci, ctx.nBuf}
		*pCodec = unsafe.Pointer(cs)
		codecMu.Lock()
		defer codecMu.Unlock()
		if codecState == nil {
			codecState = make(map[*codec]struct{}, 8)
		}
		codecState[cs] = struct{}{}
	}
	return C.SQLITE_OK
}

//export go_codec_reserve
func go_codec_reserve(pCodec unsafe.Pointer) C.int {
	return C.int((*codec)(pCodec).Reserve())
}

//export go_codec_resize
func go_codec_resize(pCodec unsafe.Pointer, nBuf, nRes C.int) {
	cs := (*codec)(pCodec)
	cs.pageSize = nBuf
	cs.Resize(int(nBuf), int(nRes))
}

//export go_codec_exec
func go_codec_exec(pCodec, pData unsafe.Pointer, pgno uint32, op C.int) unsafe.Pointer {
	cs := (*codec)(pCodec)
	page := goBytes(pData, cs.pageSize)
	var err *Error
	if op&4 == 0 {
		err = cs.Decode(page, pgno, int(op))
	} else {
		page, err = cs.Encode(page, pgno, int(op))
	}
	if err == nil {
		// TODO: Possible C.Free Required
		return C.CBytes(page)
	}

	return nil // Can't do anything with the error at the moment
}

//export go_codec_get_key
func go_codec_get_key(pCodec unsafe.Pointer, pKey *unsafe.Pointer, nKey *C.int) {
	if key := (*codec)(pCodec).Key(); len(key) > 0 {
		*pKey = cBytes(key)
		*nKey = C.int(len(key))
	}
}

//export go_codec_free
func go_codec_free(pCodec unsafe.Pointer) {
	cs := (*codec)(pCodec)
	codecMu.Lock()
	delete(codecState, cs)
	codecMu.Unlock()
	cs.Free()
	cs.Codec = nil
}

// parseKey extracts the codec name, options, and anything left over from a key
// in the format "<name>:<options>:<tail...>".
func parseKey(key []byte) (name string, opts map[string]string, tail []byte) {
	k := bytes.SplitN(key, []byte{':'}, 3)
	name = string(k[0])
	opts = make(map[string]string)
	if len(k) > 1 && len(k[1]) > 0 {
		for _, opt := range strings.Split(string(k[1]), ",") {
			if i := strings.Index(opt, "="); i > 0 {
				opts[opt[:i]] = opt[i+1:]
			} else {
				opts[opt] = ""
			}
		}
	}
	if len(k) > 2 && len(k[2]) > 0 {
		tail = k[2]
	}
	return
}

// hkdf implements the HMAC-based Key Derivation Function, as described in RFC
// 5869. The extract step is skipped if salt == nil. It is the caller's
// responsibility to set salt "to a string of HashLen zeros," if such behavior
// is desired. It returns the function that performs the expand step using the
// provided info value, which must be appendable. The derived key is valid until
// the next expansion.
func hkdf(ikm, salt []byte, dkLen int, h func() hash.Hash) func(info []byte) []byte {
	if salt != nil {
		prf := hmac.New(h, salt)
		prf.Write(ikm)
		ikm = prf.Sum(nil)
	}
	prf := hmac.New(h, ikm)
	hLen := prf.Size()
	n := (dkLen + hLen - 1) / hLen
	dk := make([]byte, dkLen, n*hLen)

	return func(info []byte) []byte {
		info = append(info, 0)
		ctr := &info[len(info)-1]
		for i, t := 1, dk[:0]; i <= n; i++ {
			*ctr = byte(i)
			prf.Reset()
			prf.Write(t)
			prf.Write(info)
			t = prf.Sum(t[len(t):])
		}
		return dk
	}
}

// rnd fills b with bytes from a CSPRNG.
func rnd(b []byte) bool {
	_, err := io.ReadFull(rand.Reader, b)
	return err == nil
}

// wipe overwrites b with zeros.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// suiteId constructs a canonical cipher suite identifier.
type suiteId struct {
	Cipher  string
	KeySize string
	Mode    string
	MAC     string
	Hash    string
	Trunc   string
}

func (s *suiteId) Id() []byte {
	id := make([]byte, 0, 64)
	section := func(parts ...string) {
		for i, p := range parts {
			if p != "" {
				parts = parts[i:]
				goto write
			}
		}
		return
	write:
		if len(id) > 0 {
			id = append(id, ',')
		}
		id = append(id, parts[0]...)
		for _, p := range parts[1:] {
			if p != "" {
				id = append(id, '-')
				id = append(id, p...)
			}
		}
	}
	section(s.Cipher, s.KeySize, s.Mode)
	section(s.MAC, s.Hash, s.Trunc)
	return id
}
