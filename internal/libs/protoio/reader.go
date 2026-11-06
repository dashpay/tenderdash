// Protocol Buffers for Go with Gadgets
//
// Copyright (c) 2013, The GoGo Authors. All rights reserved.
// http://github.com/cosmos/gogoproto
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
//     * Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above
// copyright notice, this list of conditions and the following disclaimer
// in the documentation and/or other materials provided with the
// distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
//
// Modified from original GoGo Protobuf to not buffer the reader.

package protoio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/cosmos/gogoproto/proto"
)

// ErrMsgExceedsMaxSize is returned by ReadMsg when the declared message length
// exceeds the reader's maxSize. Callers that pass a maxSize to bound what an
// unauthenticated peer can make them allocate can classify the rejection with
// errors.Is instead of matching on the message text.
var ErrMsgExceedsMaxSize = errors.New("message exceeds max size")

// ErrMalformedMsg marks a frame whose bytes were read but do not form a valid
// message: an overflowing or out-of-range length prefix, or a body that fails
// to unmarshal. Reader I/O failures and ErrMsgExceedsMaxSize are never marked.
var ErrMalformedMsg = errors.New("malformed message")

// NewDelimitedReader reads varint-delimited Protobuf messages from a reader.
// Unlike the gogoproto NewDelimitedReader, this does not buffer the reader,
// which may cause poor performance but is necessary when only reading single
// messages (e.g. in the p2p package). It also returns the number of bytes
// read, which is necessary for the p2p package.
func NewDelimitedReader(r io.Reader, maxSize int) ReadCloser {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}
	return &varintReader{r, nil, maxSize, closer}
}

type varintReader struct {
	r       io.Reader
	buf     []byte
	maxSize int
	closer  io.Closer
}

func (r *varintReader) ReadMsg(msg proto.Message) (int, error) {
	// ReadUvarint needs an io.ByteReader, and we also need to keep track of the
	// number of bytes read, so we use our own byteReader. This can't be
	// buffered, so the caller should pass a buffered io.Reader to avoid poor
	// performance.
	byteReader := newByteReader(r.r)
	l, err := binary.ReadUvarint(byteReader)
	n := byteReader.bytesRead
	if err != nil {
		if byteReader.err == nil {
			// The bytes arrived but encode no valid varint (overflow).
			err = fmt.Errorf("%w: %w", ErrMalformedMsg, err)
		}
		return n, err
	}

	// Make sure length doesn't overflow the native int size (e.g. 32-bit),
	// and that the returned sum of n+length doesn't overflow either.
	length := int(l)
	if l >= uint64(^uint(0)>>1) || length < 0 || n+length < 0 {
		return n, fmt.Errorf("%w: invalid out-of-range message length %v", ErrMalformedMsg, l)
	}
	if length > r.maxSize {
		return n, fmt.Errorf("%w (%v > %v)", ErrMsgExceedsMaxSize, length, r.maxSize)
	}

	if len(r.buf) < length {
		r.buf = make([]byte, length)
	}
	buf := r.buf[:length]
	nr, err := io.ReadFull(r.r, buf)
	n += nr
	if err != nil {
		return n, err
	}
	if err := proto.Unmarshal(buf, msg); err != nil {
		return n, fmt.Errorf("%w: %w", ErrMalformedMsg, err)
	}
	return n, nil
}

func (r *varintReader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

func UnmarshalDelimited(data []byte, msg proto.Message) error {
	_, err := NewDelimitedReader(bytes.NewReader(data), len(data)).ReadMsg(msg)
	return err
}
