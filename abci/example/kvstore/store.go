package kvstore

import (
	"bytes"
	"errors"
	"io"
	"os"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/creachadair/atomicfile"
)

// StoreFactory reads state and replaces it after a successful write.
// Not thread-safe - the caller should control concurrency.
type StoreFactory interface {
	// Reader returns new io.ReadCloser to be used to read data from
	Reader() (io.ReadCloser, error)
	// Write replaces stored state only if write completes successfully.
	Write(write func(io.Writer) error) error
}

// memStore stores state in memory.
type memStore struct {
	dbm.DB
	buf *bytes.Buffer
}

func NewMemStateStore() StoreFactory {
	return &memStore{
		buf: &bytes.Buffer{},
	}
}

func (w *memStore) Reader() (io.ReadCloser, error) {
	reader := bytes.Buffer{}
	if _, err := io.Copy(&reader, w.buf); err != nil {
		return nil, err
	}
	return io.NopCloser(&reader), nil
}

func (w *memStore) Write(write func(io.Writer) error) error {
	var next bytes.Buffer
	if err := write(&next); err != nil {
		return err
	}
	w.buf = &next
	return nil
}

type fileStore struct {
	path string
}

func NewFileStore(path string) StoreFactory {
	return &fileStore{path}
}

func (store *fileStore) Reader() (io.ReadCloser, error) {
	f, err := os.Open(store.path)
	if err != nil {
		// When file doesn't exist, we assume it's an empty (0 byte) file
		if errors.Is(err, os.ErrNotExist) {
			return io.NopCloser(&bytes.Buffer{}), nil
		}
		return nil, err
	}
	return f, nil
}

func (store *fileStore) Write(write func(io.Writer) error) error {
	return atomicfile.Tx(store.path, 0600, write)
}
