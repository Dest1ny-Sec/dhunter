package db

import "os"

// mkdirAllOS is split out so the test build can stub it if needed.
func mkdirAllOS(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
