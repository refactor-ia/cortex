//go:build darwin || linux

package modelprofile

import (
	"errors"
	"os"
	"syscall"
)

func rootStat(path string) (rootIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return rootIdentity{}, errors.New("model profile restore: inspect runtime root")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return rootIdentity{}, errors.New("model profile restore: runtime root identity unavailable")
	}
	return rootIdentity{uint64(stat.Dev), uint64(stat.Ino)}, nil
}
