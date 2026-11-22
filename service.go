package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Service struct {
	root        string
	inventories map[string]inventory
	mu          sync.Mutex
	opMu        sync.Mutex
	cancel      context.CancelFunc
	job         Job
	window      *application.WebviewWindow
}
type AppBrief struct {
	ID, Name, Version, Status string
	Translated, Missing, Kept int
	NeedsSync                 bool
}
type Job struct {
	App                                string
	Running                            bool
	Done, Total, Success, Kept, Failed int
	Message, LastError                 string
}
type Panel struct {
	ID, Name, Version, Status, Directory      string
	NeedsSync, Installed, Applied, CanRestore bool
	Pref                                      Pref
	Groups                                    []Summary
	Total                                     Summary
	Apps                                      []AppBrief
	CacheBytes                                int64
	Job                                       Job
}
type Page struct {
	Rows  []Row
	Count int
}

func makeService(root string) *Service {
	return &Service{root: root, inventories: map[string]inventory{"devin": makeInventory("devin", nil), "agy": makeInventory("agy", nil)}}
}
func (s *Service) trace(op, stage string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["operation"], fields["stage"], fields["time"] = op, stage, time.Now().Format(time.RFC3339Nano)
	_ = os.MkdirAll(filepath.Join(s.root, "cache"), 0700)
	file, err := os.OpenFile(filepath.Join(s.root, "cache", "trace.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(jsonBytes(fields), '\n'))
}
func operation() string {
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	return hex.EncodeToString(nonce[:])
}
func getTarget(id string) (target, error) {
	t, ok := targets[id]
	if !ok {
		return t, errors.New("未知应用")
	}
	return t, nil
}
func (s *Service) settings(id string) (Pref, error) {
	pref := Pref{Language: "zh-CN"}
	err := readJSON(filepath.Join(s.root, id, "settings.json"), &pref)
	if errors.Is(err, os.ErrNotExist) {
		other := "agy"
		if id == "agy" {
			other = "devin"
		}
		var sibling Pref
		if readJSON(filepath.Join(s.root, other, "settings.json"), &sibling) == nil {
			pref.Endpoint = sibling.Endpoint
			pref.Model = sibling.Model
		}
		return pref, nil
	}
	return pref, err
}
func (s *Service) markRefresh(id string) error {
	t, err := getTarget(id)
	if err != nil {
		return err
	}
	var state Identity
	if err := readJSON(filepath.Join(s.root, id, "identity.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if isRestoredIdentity(t, state) {
		return nil
	}
	return atomic(filepath.Join(s.root, id, "refresh"), []byte("requested"))
}
func (s *Service) catalog(t target) (Catalog, error) {
	value := newCatalog(t)
	err := readJSON(catalogPath(s.root, t.id), &value)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	return value, validateCatalog(value, t, s.inventories[t.id])
}
func hasLegacyDevinFiles(dir string) bool {
	for _, rel := range []string{
		"resources/app/out/vs/code/electron-browser/workbench/devin-zh-dom.js",
		devinData,
		devinOldManifest,
		devinWorkbenchScript,
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return true
		}
	}
	return false
}
func (s *Service) statusState(t target) (string, bool, bool, bool, bool) {
	dir := targetOrigin(t)
	if _, err := os.Stat(filepath.Join(dir, t.exe)); err != nil {
		return "未找到本机安装", false, false, false, false
	}
	if _, err := os.Stat(filepath.Join(s.root, t.id, "pending.json")); err == nil {
		return "有未完成的资源更新", true, true, true, true
	}
	if t.id == "devin" {
		if devinWebLocaleApplied(s.root) {
			if _, err := os.Stat(filepath.Join(s.root, t.id, "refresh")); err == nil {
				return "简体中文 · 词典待同步", true, true, true, true
			}
			return "简体中文 · 编辑器和会话", true, true, true, false
		}
		if hasLegacyDevinFiles(dir) {
			return "旧版汉化 · 待恢复原版", true, false, true, false
		}
		if s.devinLaunchLocale() == "zh-cn" {
			return "简体中文 · 语言包覆盖范围", true, true, true, false
		}
		return "英文 · 原版资源", true, false, false, false
	}
	var applied Identity
	if readJSON(filepath.Join(s.root, t.id, "identity.json"), &applied) == nil {
		if isRestoredIdentity(t, applied) {
			return "原版未汉化", true, false, false, false
		}
		if _, err := os.Stat(filepath.Join(s.root, t.id, "refresh")); err == nil {
			return "已汉化 · 待同步更新", true, true, true, true
		}
		return "已汉化", true, true, true, false
	}
	return "原版未汉化", true, false, false, false
}
func (s *Service) Panel(id string) (Panel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := getTarget(id)
	if err != nil {
		return Panel{}, err
	}
	cat, err := s.catalog(t)
	if err != nil {
		return Panel{}, err
	}
	p, err := s.settings(id)
	if err != nil {
		return Panel{}, err
	}
	groups, total := aggregate(merge(s.inventories[id], cat))
	statusText, installed, applied, canRestore, needsSync := s.statusState(t)
	apps := make([]AppBrief, 0, 2)
	for _, appID := range []string{"devin", "agy"} {
		at := targets[appID]
		if appID == id {
			apps = append(apps, AppBrief{
				ID:         appID,
				Name:       at.name,
				Version:    at.version,
				Status:     statusText,
				Translated: total.Translated,
				Missing:    total.Missing,
				Kept:       total.Kept,
				NeedsSync:  needsSync,
			})
			continue
		}
		otherStatus, _, _, _, otherSync := s.statusState(at)
		otherCat, _ := s.catalog(at)
		_, otherTotal := aggregate(merge(s.inventories[appID], otherCat))
		apps = append(apps, AppBrief{
			ID:         appID,
			Name:       at.name,
			Version:    at.version,
			Status:     otherStatus,
			Translated: otherTotal.Translated,
			Missing:    otherTotal.Missing,
			Kept:       otherTotal.Kept,
			NeedsSync:  otherSync,
		})
	}
	size, _ := s.cache(false)
	return Panel{
		ID:         t.id,
		Name:       t.name,
		Version:    t.version,
		Status:     statusText,
		Directory:  targetOrigin(t),
		NeedsSync:  needsSync,
		Installed:  installed,
		Applied:    applied,
		CanRestore: canRestore,
		Pref:       p,
		Groups:     groups,
		Total:      total,
		Apps:       apps,
		CacheBytes: size,
		Job:        s.job,
	}, nil
}
func (s *Service) SaveSettings(id string, p Pref) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := getTarget(id); err != nil {
		return err
	}
	if p.Language != "en" && p.Language != "zh-CN" {
		return errors.New("语言选项无效")
	}
	if p.Endpoint != "" {
		endpoint, err := localEndpoint(p.Endpoint)
		if err != nil {
			return err
		}
		p.Endpoint = endpoint
	}
	if len(p.Model) > 160 {
		return errors.New("模型名过长")
	}
	old, err := s.settings(id)
	if err != nil {
		return err
	}
	if err = jsonAtomic(filepath.Join(s.root, id, "settings.json"), p); err != nil {
		return err
	}
	if id == "agy" && old.Language != p.Language {
		return s.markRefresh(id)
	}
	return nil
}
func (s *Service) Find(id, query, state, group string, offset int) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := getTarget(id)
	if err != nil {
		return Page{}, err
	}
	cat, err := s.catalog(t)
	if err != nil {
		return Page{}, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := Page{Rows: []Row{}}
	offset = max(0, offset)
	for _, row := range merge(s.inventories[id], cat) {
		row.Group = displayGroup(row.Group)
		if state != "" && row.State != state || group != "" && row.Group != group || q != "" && !strings.Contains(strings.ToLower(row.Key+" "+row.Source+" "+row.Text), q) {
			continue
		}
		if out.Count >= offset && len(out.Rows) < 80 {
			out.Rows = append(out.Rows, row)
		}
		out.Count++
	}
	return out, nil
}
func (s *Service) SaveEntry(id, key, text string, keep, reset bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Running && s.job.App == id {
		return errors.New("请等当前应用的翻译任务结束后再修改词条")
	}
	t, err := getTarget(id)
	if err != nil {
		return err
	}
	base, ok := s.inventories[id].byKey[key]
	if !ok {
		return errors.New("未找到对应词条")
	}
	cat, err := s.catalog(t)
	if err != nil {
		return err
	}
	switch {
	case reset:
		delete(cat.Messages, key)
	case keep:
		if base.DefaultState == "kept" {
			delete(cat.Messages, key)
		} else {
			cat.Messages[key] = Entry{Source: base.Source, Keep: true}
		}
	default:
		if base.DefaultState == "kept" {
			return errors.New("该词条属于受保护专有标识，固定保留原文")
		}
		normalized := strings.Trim(strings.ReplaceAll(text, "\r\n", "\n"), " \t")
		if err := validateEntryText(base.Source, normalized); err != nil {
			return err
		}
		if normalized == strings.TrimSpace(base.Source) {
			return errors.New("译文与英文原文相同；如需保留英文请点「保留原文」")
		}
		if base.DefaultState == "translated" && normalized == base.Text {
			delete(cat.Messages, key)
		} else {
			cat.Messages[key] = Entry{Source: base.Source, Text: normalized}
		}
	}
	if err = jsonAtomic(catalogPath(s.root, id), cat); err != nil {
		return err
	}
	return s.markRefresh(id)
}
func (s *Service) Scan(id string) (int, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	t, err := getTarget(id)
	if err != nil {
		return 0, err
	}
	op := operation()
	s.trace(op, "scan.begin", map[string]any{"app": id})
	if err = supported(s.root, t); err != nil {
		return 0, err
	}
	inv := makeInventory(id, nil)
	keys := len(inv.rows)
	groups, total := aggregate(inv.rows)
	s.mu.Lock()
	s.inventories[id] = inv
	_ = jsonAtomic(filepath.Join(s.root, "cache", id+"-scan.json"), struct {
		Total  Summary
		Groups []Summary
	}{total, groups})
	s.mu.Unlock()
	s.trace(op, "scan.complete", map[string]any{"keys": keys, "version": t.version})
	return keys, nil
}
func (s *Service) manualImport(id, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Running && s.job.App == id {
		return errors.New("请等本次翻译结束后再导入")
	}
	t, err := getTarget(id)
	if err != nil {
		return err
	}
	var value Catalog
	if err = readJSON(path, &value); err != nil {
		return err
	}
	if err = validateCatalog(value, t, s.inventories[id]); err != nil {
		return err
	}
	if err = jsonAtomic(catalogPath(s.root, id), value); err != nil {
		return err
	}
	return s.markRefresh(id)
}
func (s *Service) manualExport(id, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := getTarget(id)
	if err != nil {
		return err
	}
	cat, err := s.catalog(t)
	if err != nil {
		return err
	}
	return jsonAtomic(path, exportCatalog(merge(s.inventories[id], cat), cat))
}
func (s *Service) Apply(id string, restore bool) (string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	t, err := getTarget(id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.job.Running && s.job.App == id {
		s.mu.Unlock()
		return "", errors.New("请先等当前应用的翻译结束后再应用或恢复")
	}
	pref, err := s.settings(id)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	cat, catErr := s.catalog(t)
	if catErr != nil && !restore {
		s.mu.Unlock()
		return "", catErr
	}
	rows := merge(s.inventories[id], cat)
	s.mu.Unlock()
	if id == "devin" {
		return s.applyDevinLocale(t, rows, restore)
	}

	op := operation()
	s.trace(op, "apply.begin", map[string]any{"app": id, "restore": restore})
	running, err := prepare(s.root, t, rows, pref, restore)
	if err == nil {
		s.mu.Lock()
		if e := os.Remove(filepath.Join(s.root, id, "refresh")); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = e
		}
		s.mu.Unlock()
	}
	s.trace(op, "apply.complete", map[string]any{"success": err == nil, "running": running})
	if err != nil {
		return "", err
	}
	if restore {
		if running {
			return fmt.Sprintf("已写入英文原版，在 %s 内重载窗口或重启后生效", t.name), nil
		}
		return "已恢复英文原版", nil
	}
	if running {
		return fmt.Sprintf("已写入汉化，在 %s 内重载窗口或重启后生效", t.name), nil
	}
	return "已应用汉化", nil
}
func (s *Service) Open(id string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	t, err := getTarget(id)
	if err != nil {
		return err
	}
	dir := targetOrigin(t)
	if _, err = os.Stat(filepath.Join(dir, t.exe)); err != nil {
		return errors.New("未找到本机安装")
	}
	if _, err = os.Stat(filepath.Join(s.root, id, "pending.json")); err == nil {
		return errors.New("有中断的操作，请先点应用汉化或恢复原版")
	}
	if running, err := appRunning(dir, t); err != nil {
		return err
	} else if running {
		return fmt.Errorf("%s 已在运行；如需加载最新词典，请在软件内重载窗口或重启", t.name)
	}
	if _, err := os.Stat(filepath.Join(s.root, id, "refresh")); id != "devin" && err == nil {
		s.mu.Lock()
		if s.job.Running && s.job.App == id {
			s.mu.Unlock()
			return errors.New("请先等当前应用的翻译结束后再启动应用")
		}
		p, err := s.settings(id)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		cat, err := s.catalog(t)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		rows := merge(s.inventories[id], cat)
		s.mu.Unlock()
		if _, err = prepare(s.root, t, rows, p, false); err != nil {
			return err
		}
		s.mu.Lock()
		err = os.Remove(filepath.Join(s.root, id, "refresh"))
		s.mu.Unlock()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	op := operation()
	s.trace(op, "open.begin", map[string]any{"app": id})
	var args []string
	if id == "devin" {
		if err := verifyDevinInstall(dir); err != nil {
			s.trace(op, "open.integrity", map[string]any{"success": false, "error": err.Error()})
			return err
		}
		s.trace(op, "open.integrity", map[string]any{"success": true})
		args = devinLocaleArgs(s.devinLaunchLocale())
	}
	cmd, err := launch(dir, t, args...)
	if err != nil {
		s.trace(op, "open.complete", map[string]any{"success": false, "error": err.Error()})
		return err
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		if waitErr != nil {
			s.trace(op, "open.complete", map[string]any{"success": false, "pid": pid, "error": waitErr.Error()})
			return fmt.Errorf("启动 %s 失败：%w", t.name, waitErr)
		}
		if stillRunning, _ := appRunning(dir, t); !stillRunning {
			s.trace(op, "open.complete", map[string]any{"success": false, "pid": pid, "error": "exited_immediately"})
			return fmt.Errorf("%s 启动后立即退出", t.name)
		}
	case <-time.After(650 * time.Millisecond):
	}
	s.trace(op, "open.complete", map[string]any{"success": true, "pid": pid, "phase": "process_started"})
	return nil
}
func (s *Service) cache(remove bool) (int64, error) {
	base := filepath.Join(s.root, "cache")
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("缓存路径无效")
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, err
	}
	var size int64
	for _, entry := range entries {
		if entry.Name() != "trace.jsonl" && entry.Name() != "devin-scan.json" && entry.Name() != "agy-scan.json" {
			continue
		}
		path, err := noLinks(base, entry.Name())
		if err != nil {
			return size, err
		}
		info, err := entry.Info()
		if err != nil {
			return size, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if remove {
			if err = os.Remove(path); err != nil {
				return size, err
			}
		}
		size += info.Size()
	}
	return size, nil
}
func (s *Service) Clean() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Running {
		return 0, errors.New("请等翻译结束后再清理缓存")
	}
	return s.cache(true)
}
func (s *Service) ServiceShutdown() error { s.Cancel(); return nil }

func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *Service) DetectModel(customEndpoint string) (ModelProbe, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	probe, _, _ := detectLocalModel(ctx, customEndpoint)
	return probe, nil
}
func (s *Service) Translate(id string, limit int, group string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Running {
		return errors.New("正在翻译另一批词条")
	}
	t, err := getTarget(id)
	if err != nil {
		return err
	}
	cat, err := s.catalog(t)
	if err != nil {
		return err
	}
	pref, err := s.settings(id)
	if err != nil {
		return err
	}
	var selected []Row
	for _, row := range merge(s.inventories[id], cat) {
		if row.State == "missing" && (group == "" || displayGroup(row.Group) == group) {
			selected = append(selected, row)
		}
	}
	if limit > 0 && len(selected) > limit {
		selected = selected[:limit]
	}
	if len(selected) == 0 {
		return errors.New("没有待翻译词条")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.job = Job{App: id, Running: true, Total: len(selected), Message: "正在连接本机翻译模型"}
	go s.runTranslation(ctx, t, pref, cat, selected)
	return nil
}
func (s *Service) runTranslation(ctx context.Context, t target, pref Pref, cat Catalog, rows []Row) {
	op := operation()
	s.trace(op, "model.start", map[string]any{"app": t.id, "total": len(rows)})
	conn, stop, err := useModel(ctx, s.root, pref, func(stageMsg string) {
		s.mu.Lock()
		s.job.Message = stageMsg
		s.mu.Unlock()
	})
	var saveErr error
	if err == nil {
		s.mu.Lock()
		s.job.Message = "正在翻译"
		s.mu.Unlock()
		lookup := map[string]Row{}
		for _, row := range rows {
			lookup[row.Key] = row
		}
		translateBatch(ctx, conn, rows, func(item modelResult) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.job.Done++
			traceFields := map[string]any{"key": item.Key, "request": item.Request, "ok": item.Err == nil, "kept": item.Keep}
			if item.Err != nil {
				traceFields["error"] = item.Err.Error()
			}
			s.trace(op, "model.reply", traceFields)
			if item.Err != nil {
				s.job.Failed++
				s.job.LastError = item.Err.Error()
				return
			}
			if item.Keep {
				cat.Messages[item.Key] = Entry{Source: lookup[item.Key].Source, Keep: true}
				s.job.Kept++
			} else {
				cat.Messages[item.Key] = Entry{Source: lookup[item.Key].Source, Text: item.Text}
			}
			s.job.Success++
			if s.job.Success%20 == 0 {
				if e := jsonAtomic(catalogPath(s.root, t.id), cat); e != nil {
					saveErr = e
					if s.cancel != nil {
						s.cancel()
					}
				}
			}
		})
		stop()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && saveErr != nil {
		err = saveErr
	}
	if s.job.Success > 0 && err == nil {
		err = jsonAtomic(catalogPath(s.root, t.id), cat)
		if err == nil {
			err = s.markRefresh(t.id)
		}
	}
	switch {
	case err != nil:
		s.job.Message = err.Error()
	case ctx.Err() != nil:
		s.job.Message = fmt.Sprintf("翻译已停止，已处理 %d 条", s.job.Success)
	default:
		if s.job.Kept > 0 {
			s.job.Message = fmt.Sprintf("已补译 %d 条，保留专有词 %d 条", s.job.Success-s.job.Kept, s.job.Kept)
		} else {
			s.job.Message = fmt.Sprintf("已补译 %d 条", s.job.Success)
		}
		if s.job.Failed > 0 {
			s.job.Message += fmt.Sprintf("，%d 条待复核", s.job.Failed)
		}
	}
	s.job.Running = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.trace(op, "model.complete", map[string]any{"accepted": s.job.Success, "kept": s.job.Kept, "failed": s.job.Failed, "saved": err == nil})
}
