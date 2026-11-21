//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func configureHidden(_ *exec.Cmd) {}
func isReparsePath(_ string) bool   { return false }
func showFatal(message string)      { fmt.Fprintln(os.Stderr, message) }

func unprotectLocalSecret(stored string) (string, error) {
	if strings.HasPrefix(stored, "dpapi:") {
		return "", errors.New("当前平台不支持 DPAPI 令牌解密")
	}
	return stored, nil
}

func discoverInstallDir(_, _ string) string { return "" }
