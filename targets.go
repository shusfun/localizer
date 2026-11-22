package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type target struct{ id, name, version, exe string }

var targets = map[string]target{
	"devin": {"devin", "Devin", "3.14.30833", "Devin.exe"},
	"agy":   {"agy", "Antigravity", "2.34.0", "Antigravity.exe"},
}

const devinOldManifest = devinAssets + "local-locale-manifest.js"

var devinPrevious = map[string]string{
	devinEntry:       "a3128d4d3bf23f6f8535977baef074509d4a35e7142f7da654c3e1d48901d660",
	devinProduct:     "3a60d6a4a0aae9206fc2e-adca754d97cbe0e471ad9d98274a0c12f2a98b3646d",
	devinData:        "ff5564b30d7725ab9f03a175937ecb59a59bcfaad84d074b710391cc80289ac8",
	devinOldManifest: "8f39a14b0e920230a9660d1f53693803f42ce4e421b32dfa3a1a1f76a31616bc",
}
var devinKnownLegacy = map[string]string{
	devinEntry: "2977326a1f920d204a87bd75cfdecd1834c76bdbc059dd77ef13f91b0f9858a6",
}

const devinLegacyRuntimeHash = "37f3cbbf0dc33771e25c303ee2cd31be0240b8cbbaa31ea642535e8848e6c1da"

func targetOrigin(t target) string {
	if base := os.Getenv("LOCALIZER_QA_ROOT"); base != "" {
		if t.id == "devin" {
			return filepath.Join(base, "devtools", "Windsurf")
		}
		return filepath.Join(base, "Apps", "Antigravity")
	}
	if override := os.Getenv("LOCALIZER_" + strings.ToUpper(t.id) + "_DIR"); override != "" {
		return filepath.Clean(override)
	}
	if found := discoverInstallDir(t.id, t.exe); found != "" {
		return found
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "Programs", t.name)
	}
	return t.name
}

func isRestoredIdentity(t target, applied Identity) bool {
	if t.id == "devin" {
		return applied.Digests[devinEntry] == devinEntryHash &&
			applied.Digests[devinWorkbenchHTML] == devinWorkbenchHTMLHash &&
			applied.Digests[devinSessionsHTML] == devinSessionsHTMLHash
	}
	return applied.Digests[agyArchive] == agyArchiveHash
}

type Identity struct{ Digests map[string]string }
type transition struct {
	Name, Before, After string
	Remove              bool
}
type journal struct {
	Pending []transition
	Final   Identity
}

func hidden(exe string, args ...string) *exec.Cmd {
	command := exec.Command(exe, args...)
	configureHidden(command)
	return command
}
func dataFor(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sum.Sum(nil)), nil
}
func digest(data []byte) string {
	if data == nil {
		return ""
	}
	return hash(data)
}
func atomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".localizer-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func atomicCopyFile(src, dst, wantHash string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".localizer-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	sum := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, sum), in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if wantHash != "" && fmt.Sprintf("%x", sum.Sum(nil)) != wantHash {
		return errors.New("原始资源指纹不匹配")
	}
	return os.Rename(out.Name(), dst)
}
func jsonAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomic(path, append(data, '\n'))
}
func noLinks(base, name string) (string, error) {
	path := base
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", errors.New("资源路径无效")
		}
		path = filepath.Join(path, part)
		if isReparsePath(path) {
			return "", fmt.Errorf("路径包含链接：%s", path)
		}
	}
	return path, nil
}
func backupFilePath(root, id, rel string) (string, error) {
	return noLinks(filepath.Join(root, id, "originals"), rel)
}
func legacyDevinBackup(origin, name, want string) ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var references map[string]*string
	backupRoot := filepath.Join(home, ".devin", "devin-zh-backups", "files-v2")
	if err = readJSON(filepath.Join(backupRoot, "manifest.json"), &references); err != nil {
		return nil, fmt.Errorf("无法读取现有 Devin 原始资源备份：%w", err)
	}
	wantPath := filepath.Join(origin, name)
	for key, value := range references {
		if !strings.EqualFold(filepath.Clean(key), wantPath) || value == nil || !strings.EqualFold(filepath.Dir(*value), backupRoot) {
			continue
		}
		data, err := os.ReadFile(*value)
		if err == nil && (want == "" || hash(data) == want) {
			return data, nil
		}
	}
	return nil, fmt.Errorf("未找到同版本原始 Devin 文件：%s", filepath.Base(name))
}
func ensureOriginal(root string, t target, name, want string) ([]byte, error) {
	backupPath, err := backupFilePath(root, t.id, name)
	if err != nil {
		return nil, err
	}
	if cached, err := dataFor(backupPath); err == nil && cached != nil && digest(cached) == want {
		return cached, nil
	}
	origin := targetOrigin(t)
	livePath, err := noLinks(origin, name)
	if err != nil {
		return nil, err
	}
	if live, err := dataFor(livePath); err == nil && live != nil && digest(live) == want {
		if err = atomic(backupPath, live); err != nil {
			return nil, err
		}
		return live, nil
	}
	if t.id == "devin" {
		data, err := legacyDevinBackup(origin, name, want)
		if err != nil {
			return nil, err
		}
		if err = atomic(backupPath, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, fmt.Errorf("未找到 %s %s 的原始资源备份（%s），请重新安装原版后再试", t.name, t.version, filepath.Base(name))
}
func ensureOriginalDigest(root string, t target, name, want string) error {
	backupPath, err := backupFilePath(root, t.id, name)
	if err != nil {
		return err
	}
	if got, err := fileDigest(backupPath); err == nil && got == want {
		return nil
	}
	origin := targetOrigin(t)
	livePath, err := noLinks(origin, name)
	if err != nil {
		return err
	}
	if got, err := fileDigest(livePath); err == nil && got == want {
		return atomicCopyFile(livePath, backupPath, want)
	}
	if t.id == "devin" {
		_, err := ensureOriginal(root, t, name, want)
		return err
	}
	return fmt.Errorf("仅支持 %s %s，安装目录资源指纹不匹配且无本地原始备份", t.name, t.version)
}
func supported(root string, t target) error {
	origin := targetOrigin(t)
	if t.id == "devin" {
		var p struct {
			WindsurfVersion string `json:"windsurfVersion"`
		}
		if err := readJSON(filepath.Join(origin, devinProduct), &p); err != nil {
			return err
		}
		if p.WindsurfVersion == "" {
			raw, err := ensureOriginal(root, t, devinProduct, devinProductHash)
			if err != nil {
				return err
			}
			if err = parseJSON(raw, &p); err != nil {
				return err
			}
		}
		if p.WindsurfVersion != t.version {
			return fmt.Errorf("仅支持 Devin %s，当前版本 %s", t.version, p.WindsurfVersion)
		}
		for name, want := range map[string]string{
			devinEntry:         devinEntryHash,
			devinProduct:       devinProductHash,
			devinWorkbenchHTML: devinWorkbenchHTMLHash,
			devinSessionsHTML:  devinSessionsHTMLHash,
		} {
			if err := ensureOriginalDigest(root, t, name, want); err != nil {
				return err
			}
		}
	} else {
		serverDigest, err := fileDigest(filepath.Join(origin, "resources/bin/language_server.exe"))
		if err != nil {
			return err
		}
		if serverDigest != agyServerHash {
			return fmt.Errorf("仅支持 Antigravity %s，资源指纹不同", t.version)
		}
		if err := ensureOriginalDigest(root, t, agyArchive, agyArchiveHash); err != nil {
			return err
		}
	}
	return nil
}
func isStockInstall(dir string, t target) bool {
	if t.id == "devin" {
		for name, want := range map[string]string{
			devinEntry:         devinEntryHash,
			devinProduct:       devinProductHash,
			devinWorkbenchHTML: devinWorkbenchHTMLHash,
			devinSessionsHTML:  devinSessionsHTMLHash,
		} {
			got, err := fileDigest(filepath.Join(dir, name))
			if err != nil || got != want {
				return false
			}
		}
		for _, extra := range []string{devinData, devinOldManifest, devinWorkbenchScript, devinSessionsScript} {
			if _, err := os.Stat(filepath.Join(dir, extra)); err == nil {
				return false
			}
		}
		return true
	}
	got, err := fileDigest(filepath.Join(dir, agyArchive))
	return err == nil && got == agyArchiveHash
}
func appRunning(dir string, t target) (bool, error) {
	if runtime.GOOS != "windows" || os.Getenv("LOCALIZER_QA_ROOT") != "" {
		return false, nil
	}
	script := `$ErrorActionPreference='Stop'; $procs=Get-CimInstance Win32_Process -Filter ("Name='"+$env:LOCALIZER_PROCESS_NAME+"'"); foreach($p in $procs){if($p.ExecutablePath -and [string]::Equals($p.ExecutablePath,$env:LOCALIZER_PROCESS_PATH,[StringComparison]::OrdinalIgnoreCase)){Write-Output 'running'; break}}`
	cmd := hidden("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "LOCALIZER_PROCESS_NAME="+t.exe, "LOCALIZER_PROCESS_PATH="+filepath.Join(dir, t.exe))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)) == "running", err
}
func currentIdentity(root, dir string, t target) (Identity, error) {
	identity := Identity{Digests: map[string]string{}}
	state := filepath.Join(root, t.id, "identity.json")
	if err := readJSON(state, &identity); err == nil {
		for name, recorded := range identity.Digests {
			path, err := noLinks(dir, name)
			if err != nil {
				return identity, err
			}
			actual, err := fileDigest(path)
			if err != nil {
				return identity, err
			}
			if t.id == "devin" && name == devinOldManifest && actual != "" {
				current, readErr := dataFor(path)
				if readErr != nil {
					return identity, readErr
				}
				expected := []byte("export default [{code:\"zh-CN\",label:\"简体中文\",translation:()=>import(\"./local-locale-zh-CN.js\").then(module=>module.default)}];\n")
				if !bytes.Equal(current, expected) {
					return identity, errors.New("Devin 语言清单内容已变动")
				}
				if err = os.Remove(path); err != nil {
					return identity, err
				}
				actual = ""
			}
			if actual == recorded {
				continue
			}
			baseline := ""
			if t.id == "devin" {
				switch name {
				case devinEntry:
					baseline = devinEntryHash
				case devinProduct:
					baseline = devinProductHash
				case devinData, devinOldManifest, devinWorkbenchScript, devinSessionsScript:
					baseline = ""
				case devinWorkbenchHTML:
					baseline = devinWorkbenchHTMLHash
				case devinSessionsHTML:
					baseline = devinSessionsHTMLHash
				}
				if name == devinEntry && actual == devinKnownLegacy[name] || name == devinOldManifest && actual == "" || (name == devinWorkbenchScript || name == devinSessionsScript) && actual == devinLegacyRuntimeHash {
					identity.Digests[name] = actual
					continue
				}
			} else if name == agyArchive {
				baseline = agyArchiveHash
			}
			if baseline == actual {
				identity.Digests[name] = actual
				continue
			}
			return identity, fmt.Errorf("应用文件指纹已外部变动：%s", filepath.Base(path))
		}
		return identity, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return identity, err
	}
	if t.id == "devin" {
		for _, name := range []string{devinEntry, devinProduct, devinData, devinOldManifest, devinWorkbenchHTML, devinSessionsHTML, devinWorkbenchScript, devinSessionsScript} {
			path, err := noLinks(dir, name)
			if err != nil {
				return identity, err
			}
			actual, err := fileDigest(path)
			if err != nil {
				return identity, err
			}
			expected := map[string]string{
				devinEntry:         devinEntryHash,
				devinProduct:       devinProductHash,
				devinWorkbenchHTML: devinWorkbenchHTMLHash,
				devinSessionsHTML:  devinSessionsHTMLHash,
			}[name]
			knownLegacy := strings.ReplaceAll(devinPrevious[name], "-", "")
			if name == devinEntry {
				knownLegacy = devinKnownLegacy[name]
			} else if name == devinWorkbenchScript || name == devinSessionsScript {
				knownLegacy = devinLegacyRuntimeHash
			}
			if actual != expected && (knownLegacy == "" || actual != knownLegacy) {
				return identity, fmt.Errorf("现有安装资源已变动：%s", filepath.Base(name))
			}
			identity.Digests[name] = actual
		}
	} else {
		actual, err := fileDigest(filepath.Join(dir, agyArchive))
		if err != nil {
			return identity, err
		}
		if actual != agyArchiveHash {
			return identity, errors.New("Antigravity 资源包已被其他工具修改，无法建立原版基线")
		}
		identity.Digests[agyArchive] = agyArchiveHash
	}
	return identity, nil
}
func allowed(t target, name string) bool {
	if t.id == "agy" {
		return name == agyArchive
	}
	return name == devinEntry || name == devinProduct || name == devinData || name == devinOldManifest || name == devinWorkbenchHTML || name == devinSessionsHTML || name == devinWorkbenchScript || name == devinSessionsScript
}
func recoverTransaction(home, dir string, t target) error {
	var state journal
	name := filepath.Join(home, "pending.json")
	if err := readJSON(name, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for i, change := range state.Pending {
		if !allowed(t, change.Name) {
			return errors.New("未完成操作的资源清单无效")
		}
		dest, err := noLinks(dir, change.Name)
		if err != nil {
			return err
		}
		actual, err := fileDigest(dest)
		if err != nil {
			return err
		}
		if actual != change.Before && actual != change.After {
			return fmt.Errorf("恢复遇到资源冲突：%s", filepath.Base(dest))
		}
		if actual != change.After && !change.Remove {
			nextDigest, err := fileDigest(filepath.Join(home, fmt.Sprintf("next-%d.tmp", i)))
			if err != nil || nextDigest != change.After {
				return errors.New("操作暂存文件不完整")
			}
		}
	}
	for i, change := range state.Pending {
		dest, err := noLinks(dir, change.Name)
		if err != nil {
			return err
		}
		actual, err := fileDigest(dest)
		if err != nil {
			return err
		}
		if actual == change.After {
			continue
		}
		if change.Remove {
			err = os.Remove(dest)
		} else {
			var next []byte
			next, err = os.ReadFile(filepath.Join(home, fmt.Sprintf("next-%d.tmp", i)))
			if err == nil {
				err = atomic(dest, next)
			}
		}
		if err != nil {
			return err
		}
		afterWrite, err := fileDigest(dest)
		if err != nil || afterWrite != change.After {
			return errors.New("写入后的校验失败")
		}
	}
	if err := jsonAtomic(filepath.Join(home, "identity.json"), state.Final); err != nil {
		return err
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	for i := range state.Pending {
		_ = os.Remove(filepath.Join(home, fmt.Sprintf("next-%d.tmp", i)))
	}
	return nil
}

func applyFiles(root, dir string, t target, changes []fileChange) error {
	home := filepath.Join(root, t.id)
	if err := recoverTransaction(home, dir, t); err != nil {
		return err
	}
	old, err := currentIdentity(root, dir, t)
	if err != nil {
		return err
	}
	for name, expected := range old.Digests {
		if !allowed(t, name) {
			return errors.New("应用的资源清单无效")
		}
		p, err := noLinks(dir, name)
		if err != nil {
			return err
		}
		got, err := fileDigest(p)
		if err != nil {
			return err
		}
		if got != expected {
			return fmt.Errorf("应用文件指纹已外部变动：%s", filepath.Base(p))
		}
	}
	state := journal{Final: old}
	for _, change := range changes {
		if !allowed(t, change.Name) {
			return errors.New("资源不在允许写入范围内")
		}
		after := digest(change.Data)
		before := old.Digests[change.Name]
		if after == before {
			continue
		}
		if change.Data != nil {
			if err := atomic(filepath.Join(home, fmt.Sprintf("next-%d.tmp", len(state.Pending))), change.Data); err != nil {
				return err
			}
		}
		state.Pending = append(state.Pending, transition{change.Name, before, after, change.Data == nil})
		state.Final.Digests[change.Name] = after
	}
	if len(state.Pending) == 0 {
		if _, err := os.Stat(filepath.Join(home, "identity.json")); errors.Is(err, os.ErrNotExist) {
			return jsonAtomic(filepath.Join(home, "identity.json"), state.Final)
		}
		return nil
	}
	if err := jsonAtomic(filepath.Join(home, "pending.json"), state); err != nil {
		return err
	}
	return recoverTransaction(home, dir, t)
}
func prepare(root string, t target, rows []Row, pref Pref, restore bool) (bool, error) {
	dir := targetOrigin(t)
	if err := supported(root, t); err != nil {
		return false, err
	}
	running, err := appRunning(dir, t)
	if err != nil {
		return false, err
	}
	if running {
		return true, fmt.Errorf("请先退出 %s，再应用或恢复汉化", t.name)
	}
	var changes []fileChange
	if t.id == "devin" {
		original, err := ensureOriginal(root, t, devinEntry, devinEntryHash)
		if err != nil {
			return running, err
		}
		product, err := ensureOriginal(root, t, devinProduct, devinProductHash)
		if err != nil {
			return running, err
		}
		workbench, err := ensureOriginal(root, t, devinWorkbenchHTML, devinWorkbenchHTMLHash)
		if err != nil {
			return running, err
		}
		sessions, err := ensureOriginal(root, t, devinSessionsHTML, devinSessionsHTMLHash)
		if err != nil {
			return running, err
		}
		if !restore {
			return false, errors.New("Devin 安装资源仅支持恢复原版")
		}
		changes = []fileChange{
			{devinProduct, product},
			{devinOldManifest, nil},
			{devinEntry, original},
			{devinData, nil},
			{devinWorkbenchHTML, workbench},
			{devinSessionsHTML, sessions},
			{devinWorkbenchScript, nil},
			{devinSessionsScript, nil},
		}
		if err := normalizeDevinInstall(root, dir); err != nil {
			return running, err
		}
	} else {
		original, err := ensureOriginal(root, t, agyArchive, agyArchiveHash)
		if err != nil {
			return false, err
		}
		if restore {
			pref = Pref{Language: "en"}
		}
		changes, err = agyPatch(original, rows, pref)
		if err != nil {
			return false, err
		}
	}
	return running, applyFiles(root, dir, t, changes)
}

func cleanDesktopEnv(environ []string) []string {
	cleaned := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(key)
		if upper == "ELECTRON_RUN_AS_NODE" ||
			upper == "ELECTRON_NO_ATTACH_CONSOLE" ||
			upper == "NODE_OPTIONS" ||
			strings.HasPrefix(upper, "VSCODE_") ||
			strings.HasPrefix(upper, "WINDSURF_") ||
			strings.HasPrefix(upper, "DEVIN_") {
			continue
		}
		cleaned = append(cleaned, kv)
	}
	return cleaned
}

func launch(dir string, t target, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(filepath.Join(dir, t.exe), args...)
	cmd.Dir = dir
	cmd.Env = cleanDesktopEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
func normalizeDevinInstall(root, dir string) error {
	marker := filepath.Join(root, "devin", "stock-ready")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, "devin", "identity.json")); err == nil {
		return atomic(marker, []byte(time.Now().Format(time.RFC3339)))
	}
	if os.Getenv("LOCALIZER_QA_ROOT") != "" {
		return atomic(marker, []byte("qa-stock"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var originals map[string]*string
	backupDir := filepath.Join(home, ".devin", "devin-zh-backups", "files-v2")
	err = readJSON(filepath.Join(backupDir, "manifest.json"), &originals)
	if errors.Is(err, os.ErrNotExist) {
		return atomic(marker, []byte("stock"))
	}
	if err != nil {
		return err
	}
	for key, backup := range originals {
		rel, err := filepath.Rel(dir, key)
		if err != nil || !strings.HasPrefix(strings.ToLower(filepath.ToSlash(rel)), "resources/app/") {
			continue
		}
		dest, err := noLinks(dir, rel)
		if err != nil {
			return err
		}
		current, err := dataFor(dest)
		if err != nil {
			return err
		}
		if backup == nil {
			patchFile := filepath.Base(rel)
			allowedPatch := strings.Contains(patchFile, "devin-zh-dom.js") || strings.HasPrefix(patchFile, "package.nls") || patchFile == "local-locale-manifest.js"
			if !allowedPatch || current == nil {
				continue
			}
			if err = os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if !strings.EqualFold(filepath.Dir(*backup), backupDir) {
			return errors.New("旧备份文件的路径无效")
		}
		data, err := os.ReadFile(*backup)
		if err != nil {
			return err
		}
		if cachePath, cacheErr := backupFilePath(root, "devin", rel); cacheErr == nil {
			_ = atomic(cachePath, data)
		}
		if bytes.Equal(data, current) {
			continue
		}
		if err = atomic(dest, data); err != nil {
			return err
		}
	}
	for _, name := range []string{devinWorkbenchScript, devinSessionsScript} {
		path, err := noLinks(dir, name)
		if err != nil {
			return err
		}
		current, err := dataFor(path)
		if err != nil || current == nil {
			continue
		}
		expected := append(append([]byte{}, asset("assets/devin-runtime.js")...), '\n')
		if !bytes.Equal(current, expected) && digest(current) != devinLegacyRuntimeHash {
			return fmt.Errorf("Devin 运行时文件已修改：%s", filepath.Base(path))
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return atomic(marker, []byte(time.Now().Format(time.RFC3339)))
}
