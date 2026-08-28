package backupjournal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type ownedPath struct {
	name      string
	info      os.FileInfo
	directory bool
}
type fsAttempt struct{ created []ownedPath }

var fileWrite = func(file *os.File, data []byte) (int, error) { return file.Write(data) }
var pathLstat = os.Lstat
var fileStat = func(file *os.File) (os.FileInfo, error) { return file.Stat() }

func ensureJournalBase(home string) (string, fsAttempt, error) {
	if !filepath.IsAbs(home) || !realDir(home, false) {
		return "", fsAttempt{}, errors.New("backup journal: unsafe home")
	}
	attempt := fsAttempt{}
	cortex := filepath.Join(home, ".cortex")
	if err := attempt.mkdir(cortex); err != nil {
		return "", attempt, err
	}
	base := filepath.Join(cortex, "transactions")
	if err := attempt.mkdir(base); err != nil {
		return "", attempt, err
	}
	return base, attempt, nil
}
func (attempt *fsAttempt) mkdir(name string) error {
	if err := os.Mkdir(name, 0700); err != nil {
		if !os.IsExist(err) || !realDir(name, true) {
			return errors.New("backup journal: unsafe directory")
		}
		return nil
	}
	attempt.created = append(attempt.created, ownedPath{name: name, directory: true})
	info, err := pathLstat(name)
	if err != nil || !realDirInfo(info, true) {
		return errors.New("backup journal: unsafe created directory")
	}
	attempt.created[len(attempt.created)-1].info = info
	return nil
}
func (attempt *fsAttempt) reserve(base, id string) (string, error) {
	if !validHash(id) || !realDir(base, true) {
		return "", errors.New("backup journal: unsafe reservation")
	}
	name := filepath.Join(base, id)
	if err := os.Mkdir(name, 0700); err != nil {
		return "", err
	}
	attempt.created = append(attempt.created, ownedPath{name: name, directory: true})
	info, err := pathLstat(name)
	if err != nil || !realDirInfo(info, true) {
		return "", errors.New("backup journal: unsafe reservation")
	}
	attempt.created[len(attempt.created)-1].info = info
	return name, nil
}
func (attempt *fsAttempt) createFile(base, relativeName string) (string, error) {
	if !relative(relativeName) || !realDir(base, true) {
		return "", errors.New("backup journal: unsafe relative path")
	}
	parts, directory := strings.Split(relativeName, "/"), base
	for _, part := range parts[:len(parts)-1] {
		directory = filepath.Join(directory, part)
		if err := attempt.mkdir(directory); err != nil {
			return "", err
		}
	}
	name := filepath.Join(directory, parts[len(parts)-1])
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	attempt.created = append(attempt.created, ownedPath{name: name})
	info, statErr := fileStat(file)
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !regular0600(info) {
		return "", errors.New("backup journal: unsafe created file")
	}
	attempt.created[len(attempt.created)-1].info = info
	return name, nil
}
func durableWrite(attempt *fsAttempt, name string, data []byte) error {
	owned, err := attempt.owned(name)
	if err != nil {
		return err
	}
	expected := append([]byte(nil), data...)
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(owned.info, opened) {
		file.Close()
		return errors.New("backup journal: write ownership drift")
	}
	for len(data) > 0 {
		n, writeErr := fileWrite(file, data)
		data = data[n:]
		if writeErr != nil || n == 0 {
			file.Close()
			attempt.remove(name)
			if writeErr != nil {
				return writeErr
			}
			return io.ErrShortWrite
		}
	}
	if err = file.Sync(); err == nil {
		err = file.Close()
	} else {
		file.Close()
	}
	if err != nil {
		attempt.remove(name)
		return err
	}
	readback, err := readRegular(name, int64(len(expected)))
	if err != nil || !bytes.Equal(readback, expected) {
		attempt.remove(name)
		if err != nil {
			return err
		}
		return errors.New("backup journal: readback mismatch")
	}
	return syncDirectory(filepath.Dir(name))
}
func readRegular(name string, limit int64) ([]byte, error) {
	if limit < 0 || limit > maxLength {
		return nil, errors.New("backup journal: invalid read limit")
	}
	before, err := os.Lstat(name)
	if err != nil || !regular0600(before) || before.Size() < 0 || before.Size() > limit {
		return nil, errors.New("backup journal: unsafe file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !regular0600(after) || !os.SameFile(before, after) {
		return nil, errors.New("backup journal: file drift")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || int64(len(data)) != before.Size() {
		return nil, errors.New("backup journal: truncated file")
	}
	return data, nil
}

// syncDirectory accepts EINVAL and ENOTSUP because those platforms do not support directory fsync.
func syncDirectory(name string) error {
	directory, err := os.Open(name)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	if err != nil {
		return err
	}
	return closeErr
}
func (attempt *fsAttempt) owned(name string) (ownedPath, error) {
	for _, owned := range attempt.created {
		if owned.name != name {
			continue
		}
		current, err := pathLstat(name)
		if err == nil && owned.matches(current) {
			return owned, nil
		}
		break
	}
	return ownedPath{}, errors.New("backup journal: cleanup ownership drift")
}
func (attempt *fsAttempt) remove(name string) error {
	for index, owned := range attempt.created {
		if owned.name != name {
			continue
		}
		if _, err := attempt.owned(name); err != nil {
			return err
		}
		if err := os.Remove(name); err != nil {
			return err
		}
		attempt.created = append(attempt.created[:index], attempt.created[index+1:]...)
		return nil
	}
	return errors.New("backup journal: cleanup unowned path")
}
func (attempt *fsAttempt) cleanup() error {
	for index := len(attempt.created) - 1; index >= 0; index-- {
		if err := attempt.remove(attempt.created[index].name); err != nil {
			return err
		}
	}
	return nil
}
func realDir(name string, strict bool) bool {
	info, err := pathLstat(name)
	return err == nil && realDirInfo(info, strict)
}
func realDirInfo(info os.FileInfo, strict bool) bool {
	return info.Mode().IsDir() && (!strict || info.Mode().Perm() == 0700)
}
func regular0600(info os.FileInfo) bool { return info.Mode().IsRegular() && info.Mode().Perm() == 0600 }
func (owned ownedPath) matches(current os.FileInfo) bool {
	if owned.info != nil {
		return sameOwned(owned, current)
	}
	return (owned.directory && realDirInfo(current, true)) || (!owned.directory && regular0600(current))
}
func sameOwned(owned ownedPath, current os.FileInfo) bool {
	return os.SameFile(owned.info, current) && ((owned.info.IsDir() && realDirInfo(current, true)) || (!owned.info.IsDir() && regular0600(current)))
}
