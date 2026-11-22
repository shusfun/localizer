package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const devinLegacyExtension = "devin-zh.devin-zh-hans"
const devinPack = "devin-language-pack.vsix"

func devinWebLocale(original []byte, rows []Row) ([]fileChange, error) {
	const languageList = "gu=[{code:"
	const endOfList = "]})))"
	const defaultLocale = "return localStorage.getItem(eR)||`en`"
	const fallbackLocale = "catch{return`en`}}function JL"
	start := bytes.Index(original, []byte(languageList))
	if start < 0 || bytes.Count(original, []byte(languageList)) != 1 ||
		bytes.Count(original, []byte(defaultLocale)) != 1 || bytes.Count(original, []byte(fallbackLocale)) != 1 {
		return nil, errors.New("Devin 会话语言入口与支持的版本不一致")
	}
	end := bytes.Index(original[start:], []byte(endOfList))
	if end < 0 {
		return nil, errors.New("Devin 会话语言列表结构不匹配")
	}
	end += start
	groups := map[string]map[string]string{}
	for _, row := range rows {
		if row.State != "translated" {
			continue
		}
		_, key, _ := strings.Cut(row.Key, ".")
		if groups[row.Group] == nil {
			groups[row.Group] = map[string]string{}
		}
		groups[row.Group][key] = row.Text
	}
	data := append([]byte("export default "), jsonBytes(groups)...)
	data = append(data, ';', '\n')
	const registration = ",{code:'zh-CN',label:'简体中文',translation:()=>import('./local-locale-zh-CN.js').then(m=>m.default)}"
	entry := make([]byte, 0, len(original)+len(registration))
	entry = append(entry, original[:end]...)
	entry = append(entry, registration...)
	entry = append(entry, original[end:]...)
	entry = bytes.Replace(entry, []byte(defaultLocale), []byte("return localStorage.getItem(eR)||`zh-CN`"), 1)
	entry = bytes.Replace(entry, []byte(fallbackLocale), []byte("catch{return`zh-CN`}}function JL"), 1)
	return []fileChange{{devinData, data}, {devinEntry, entry}}, nil
}

func verifyDevinInstall(dir string) error {
	productPath, err := noLinks(dir, devinProduct)
	if err != nil {
		return err
	}
	digest, err := fileDigest(productPath)
	if err != nil {
		return err
	}
	if digest != devinProductHash {
		return errors.New("Devin 安装清单与支持的原版不一致，请恢复原版或使用同版本官方安装包修复")
	}
	var product struct{ Checksums map[string]string }
	if err := readJSON(productPath, &product); err != nil {
		return err
	}
	const outPrefix = "resources/app/out/"
	for _, name := range []string{devinEntry, devinData} {
		if _, protected := product.Checksums[strings.TrimPrefix(name, outPrefix)]; protected {
			return errors.New("Devin 会话语言入口在原版校验清单中，无法应用会话汉化")
		}
	}
	return verifyDevinChecksums(filepath.Join(dir, "resources", "app", "out"), product.Checksums)
}

func verifyDevinChecksums(out string, checksums map[string]string) error {
	for name, expected := range checksums {
		path, err := noLinks(out, name)
		if err != nil {
			return err
		}
		actual, err := fileDigest(path)
		if err != nil {
			return fmt.Errorf("无法读取 Devin 原版资源 %s：%w", filepath.Base(name), err)
		}
		stock, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(expected, "="))
		if err != nil || actual != hex.EncodeToString(stock) {
			return fmt.Errorf("Devin 原版资源校验失败：%s，请使用官方安装包修复", filepath.Base(name))
		}
	}
	return nil
}

func devinLocaleArgs(locale string) []string {
	return []string{"--locale=" + locale, "--disable-extension", devinLegacyExtension}
}

func devinWebLocaleApplied(root string) bool {
	var identity Identity
	if err := readJSON(filepath.Join(root, "devin", "identity.json"), &identity); err != nil {
		return false
	}
	return identity.Digests[devinProduct] == devinProductHash &&
		identity.Digests[devinWorkbenchHTML] == devinWorkbenchHTMLHash &&
		identity.Digests[devinSessionsHTML] == devinSessionsHTMLHash &&
		identity.Digests[devinData] != ""
}

func (s *Service) devinLaunchLocale() string {
	var locale string
	if readJSON(filepath.Join(s.root, "devin", "launch-locale.json"), &locale) == nil && locale == "zh-cn" {
		return locale
	}
	return "en"
}

func installDevinLanguagePack(ctx context.Context, root, dir string, t target) error {
	pack := filepath.Join(root, "cache", devinPack)
	if err := atomic(pack, asset("assets/"+devinPack)); err != nil {
		return err
	}
	cli := filepath.Join(dir, "resources", "app", "out", "cli.js")
	cmd := exec.CommandContext(ctx, filepath.Join(dir, t.exe), cli, "--install-extension", pack)
	cmd.Dir = dir
	cmd.Env = append(cleanDesktopEnv(os.Environ()), "ELECTRON_RUN_AS_NODE=1")
	configureHidden(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("无法安装 Devin 中文语言包：%w（%s）", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *Service) applyDevinLocale(t target, rows []Row, restore bool) (message string, err error) {
	op := operation()
	s.trace(op, "apply.begin", map[string]any{"app": t.id, "restore": restore})
	defer func() {
		s.trace(op, "apply.complete", map[string]any{"success": err == nil})
	}()
	dir := targetOrigin(t)
	if running, err := appRunning(dir, t); err != nil {
		return "", err
	} else if running {
		return "", errors.New("请先退出 Devin，再切换启动语言")
	}
	if restore && (hasLegacyDevinFiles(dir) || devinWebLocaleApplied(s.root)) {
		if _, err := prepare(s.root, t, nil, Pref{}, true); err != nil {
			return "", fmt.Errorf("无法安全恢复 Devin 资源，请使用同版本官方安装包修复：%w", err)
		}
		s.trace(op, "apply.restore", map[string]any{"success": true})
	}
	if err := verifyDevinInstall(dir); err != nil {
		s.trace(op, "apply.integrity", map[string]any{"success": false, "error": err.Error()})
		return "", err
	}
	s.trace(op, "apply.integrity", map[string]any{"success": true})
	locale := "en"
	message = "已准备英文启动，点击「启动应用」打开 Devin"
	if !restore {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		started := time.Now()
		err := installDevinLanguagePack(ctx, s.root, dir, t)
		s.trace(op, "apply.language_pack", map[string]any{"success": err == nil, "elapsed_ms": time.Since(started).Milliseconds()})
		if err != nil {
			return "", err
		}
		original, err := ensureOriginal(s.root, t, devinEntry, devinEntryHash)
		if err != nil {
			return "", err
		}
		changes, err := devinWebLocale(original, rows)
		if err != nil {
			return "", err
		}
		err = applyFiles(s.root, dir, t, changes)
		s.trace(op, "apply.web_locale", map[string]any{"success": err == nil})
		if err != nil {
			return "", err
		}
		if err := os.Remove(filepath.Join(s.root, t.id, "refresh")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		locale = "zh-cn"
		message = "已写入中文词典。启动 Devin 后，可在「首选项 → 显示语言」选择简体中文"
	}
	if err := jsonAtomic(filepath.Join(s.root, t.id, "launch-locale.json"), locale); err != nil {
		return "", err
	}
	return message, nil
}
