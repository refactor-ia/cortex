//go:build !darwin && !linux

package modelprofile

import "errors"

func rootStat(string) (rootIdentity, error) {
	return rootIdentity{}, errors.New("model profile restore: runtime root identity unsupported on this platform")
}
