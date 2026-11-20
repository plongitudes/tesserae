//go:build !unix

package sync

import (
	"errors"
	"io/fs"
	"os"
)

// tryLock takes the lock by creating the file at path, which fails if it
// is there. Without flock, a sync that is killed leaves the file behind,
// and it has to be removed by hand.
func tryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { f.Close(); os.Remove(path) }, true, nil
}
