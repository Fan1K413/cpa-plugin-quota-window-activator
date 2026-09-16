//go:build windows

package core

import (
	"errors"
	"os"
)

func replaceFile(source, target string) error {
	err := os.Rename(source, target)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
		return err
	}
	backup := target + ".previous"
	_ = os.Remove(backup)
	if moveErr := os.Rename(target, backup); moveErr != nil && !errors.Is(moveErr, os.ErrNotExist) {
		return err
	}
	if moveErr := os.Rename(source, target); moveErr != nil {
		_ = os.Rename(backup, target)
		return moveErr
	}
	_ = os.Remove(backup)
	return nil
}
