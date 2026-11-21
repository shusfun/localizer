//go:build windows

package main

import (
	"encoding/base64"
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

func configureHidden(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func isReparsePath(path string) bool {
	attrs, err := syscall.GetFileAttributes(syscall.StringToUTF16Ptr(path))
	return err == nil && attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func showFatal(message string) {
	dll := syscall.NewLazyDLL("user32.dll")
	box := dll.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("界面汉化工具")
	_, _, _ = box.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

func unprotectLocalSecret(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, "dpapi:") {
		return stored, nil
	}
	enc, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, "dpapi:"))
	if err != nil || len(enc) == 0 {
		return "", errors.New("本机服务令牌格式无效")
	}
	in := windows.DataBlob{
		Size: uint32(len(enc)),
		Data: &enc[0],
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", errors.New("无法解密本机 Mistral 服务令牌")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	dec := unsafe.Slice(out.Data, out.Size)
	plain := string(dec)
	for i := range dec {
		dec[i] = 0
	}
	return plain, nil
}

var (
	installCacheMu sync.Mutex
	installCache   = map[string]string{}
)

func cleanRegPath(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if idx := strings.LastIndex(cleaned, ","); idx > 0 && !strings.Contains(cleaned[idx:], `\`) {
		cleaned = cleaned[:idx]
	}
	return strings.Trim(strings.TrimSpace(cleaned), `"`)
}

func discoverInstallDir(id, exeName string) string {
	installCacheMu.Lock()
	defer installCacheMu.Unlock()
	if cached, ok := installCache[id]; ok {
		return cached
	}

	checkDir := func(dir string) string {
		dir = cleanRegPath(dir)
		if dir == "" {
			return ""
		}
		if strings.EqualFold(filepath.Base(dir), exeName) {
			dir = filepath.Dir(dir)
		}
		if info, err := os.Stat(filepath.Join(dir, exeName)); err == nil && !info.IsDir() {
			return filepath.Clean(dir)
		}
		return ""
	}

	roots := []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE}
	subkeys := []string{
		`Software\Microsoft\Windows\CurrentVersion\Uninstall`,
		`Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
	}
	for _, root := range roots {
		for _, sub := range subkeys {
			k, err := registry.OpenKey(root, sub, registry.ENUMERATE_SUB_KEYS)
			if err != nil {
				continue
			}
			names, _ := k.ReadSubKeyNames(-1)
			k.Close()
			for _, name := range names {
				sk, err := registry.OpenKey(root, sub+`\`+name, registry.QUERY_VALUE)
				if err != nil {
					continue
				}
				loc, _, _ := sk.GetStringValue("InstallLocation")
				ico, _, _ := sk.GetStringValue("DisplayIcon")
				sk.Close()
				if hit := checkDir(loc); hit != "" {
					installCache[id] = hit
					return hit
				}
				if hit := checkDir(ico); hit != "" {
					installCache[id] = hit
					return hit
				}
			}
		}
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	progFiles := os.Getenv("ProgramFiles")
	var fallbacks []string
	if id == "devin" {
		fallbacks = []string{
			filepath.Join(localAppData, "Programs", "Devin"),
			filepath.Join(localAppData, "Programs", "Windsurf"),
			filepath.Join(progFiles, "Devin"),
			filepath.Join(progFiles, "Windsurf"),
		}
	} else {
		fallbacks = []string{
			filepath.Join(localAppData, "Programs", "Antigravity"),
			filepath.Join(progFiles, "Antigravity"),
		}
	}
	for _, cand := range fallbacks {
		if hit := checkDir(cand); hit != "" {
			installCache[id] = hit
			return hit
		}
	}
	return ""
}
