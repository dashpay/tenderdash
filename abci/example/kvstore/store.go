package kvstore

import (
	"bytes"
	"errors"
	"io"
	"os"

	dbm "github.com/cometbft/cometbft-db"
)

// StoreFactory is a factory that offers a reader to read data from, or writer to write data to it.
// Not thread-safe - the caller should control concurrency.
type StoreFactory interface {
	// Reader returns new io.ReadCloser to be used to read data from
	Reader() (io.ReadCloser, error)
	// Writer returns new io.WriteCloser to be used to write data to
	Writer() (io.WriteCloser, error)
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

func (w *memStore) Writer() (io.WriteCloser, error) {
	return &writerNopCloser{w.buf}, nil
}

type writerNopCloser struct{ io.Writer }

func (writerNopCloser) Close() error { return nil }

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

func (store *fileStore) Writer() (io.WriteCloser, error) {
	return os.OpenFile(store.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
}
