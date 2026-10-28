//go:build !darwin || !cgo

package main

import "errors"

func keychainSet(_, _, _ string) error {
	return errors.New("此构建未启用 macOS Security.framework 钥匙串支持")
}

func keychainGet(_, _ string) (string, error) {
	return "", errors.New("此构建未启用 macOS Security.framework 钥匙串支持")
}

func keychainDelete(_, _ string) error {
	return errors.New("此构建未启用 macOS Security.framework 钥匙串支持")
}
