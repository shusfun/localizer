package main

import (
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

func (s *Service) applyDevinLocale(t target, restore bool) (message string, err error) {
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
	if restore && !isStockInstall(dir, t) {
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
		locale = "zh-cn"
		message = "已安装中文语言包。首次启动如仍显示英文，请退出 Devin 后再点「启动应用」"
	}
	if err := jsonAtomic(filepath.Join(s.root, t.id, "launch-locale.json"), locale); err != nil {
		return "", err
	}
	return message, nil
}
