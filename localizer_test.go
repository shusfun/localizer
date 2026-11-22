package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	syncatomic "sync/atomic"
	"testing"
	"time"
)

func TestCatalogRoundTrip(t *testing.T) {
	for _, id := range []string{"devin", "agy"} {
		inv := makeInventory(id, nil)
		cat := exportCatalog(inv.rows, newCatalog(targets[id]))
		if err := validateCatalog(cat, targets[id], inv); err != nil {
			t.Fatal(err)
		}
		_, summary := aggregate(merge(inv, cat))
		if summary.Translated == 0 {
			t.Fatalf("invalid summary: %+v", summary)
		}
		if summary.Missing != 0 {
			t.Fatalf("expected %s built-in catalog to have 0 missing entries, got: %+v", id, summary)
		}
		t.Logf("%s: %+v", id, summary)
		cat.Messages["unknown"] = Entry{"Test", "测试", false}
		if validateCatalog(cat, targets[id], inv) == nil {
			t.Fatal("unknown key was accepted")
		}
	}
	if !validText("Launch <code>git status</code> for {{name}}", "为 {{name}} 启动 <code>git status</code>") {
		t.Fatal("reordered named placeholder and inline code rejected")
	}
	if !validText("Launch <code>git status</code> for {{name}}", "启动 <code>git status</code>，用于 {{name}}") {
		t.Fatal("valid inline code and placeholder rejected")
	}
	if !validText("Please enter a valid URL (e.g., https://example.com/icon.png)", "请输入有效的 URL（例如 https://example.com/icon.png）") {
		t.Fatal("URL followed by fullwidth paren rejected")
	}
	if !validText("Enter a full URL that starts with https://, or a path.", "输入以 https:// 开头的完整 URL，或路径。") {
		t.Fatal("bare https:// scheme rejected")
	}
	if validText("Launch <code>git status</code> for {{name}}", "启动 <code>git clean</code>，用于 {{person}}") {
		t.Fatal("changed protected fragments accepted")
	}
	if validText("Click <limitButton>raise</limitButton> now", "立即 </limitButton>提高<limitButton>") {
		t.Fatal("inverted tag order accepted")
	}
}

func TestSaveEntryAndRefreshState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALIZER_QA_ROOT", root)
	s := makeService(root)

	var targetRow Row
	for _, r := range s.inventories["devin"].rows {
		if strings.Contains(r.Source, "{{") && r.DefaultState != "kept" {
			targetRow = r
			break
		}
	}
	if targetRow.Key == "" {
		t.Fatal("expected a Devin entry with {{placeholder}}")
	}

	if err := s.SaveEntry("devin", targetRow.Key, "缺少占位符的译文", false, false); err == nil || !strings.Contains(err.Error(), "受保护片段") {
		t.Fatalf("expected placeholder validation error, got: %v", err)
	}

	tokens := tokenPattern.FindAllString(targetRow.Source, -1)
	validCustom := "测试译文 " + strings.Join(tokens, " ")
	for strings.Count(validCustom, "\n") < strings.Count(targetRow.Source, "\n") {
		validCustom += "\n"
	}

	agyDir := filepath.Join(root, "Apps", "Antigravity")
	if err := atomic(filepath.Join(agyDir, "Antigravity.exe"), []byte("exe")); err != nil {
		t.Fatal(err)
	}

	// Unpatched install starts as 原版未汉化 with Applied=false, CanRestore=false.
	initialPanel, err := s.Panel("agy")
	if err != nil {
		t.Fatal(err)
	}
	if initialPanel.Status != "原版未汉化" || initialPanel.Applied || initialPanel.CanRestore {
		t.Fatalf("expected clean stock status, got %+v", initialPanel)
	}

	// Patched install transitions to 已汉化 · 待同步更新 when dictionary changes.
	if err := jsonAtomic(filepath.Join(root, "agy", "identity.json"), Identity{Digests: map[string]string{agyArchive: "patched-digest"}}); err != nil {
		t.Fatal(err)
	}

	var agyEditable, agyProtected Row
	for _, r := range s.inventories["agy"].rows {
		if agyEditable.Key == "" && r.DefaultState == "translated" && !strings.Contains(r.Source, "\n") {
			agyEditable = r
		}
		if agyProtected.Key == "" && r.DefaultState == "kept" {
			agyProtected = r
		}
	}
	if err := s.SaveEntry("agy", agyProtected.Key, "视窗", false, false); err == nil {
		t.Fatal("expected protected kept entry to reject custom translation")
	}
	if err := s.SaveEntry("agy", agyEditable.Key, "自定义测试译文", false, false); err != nil {
		t.Fatalf("save custom entry: %v", err)
	}
	panel, err := s.Panel("agy")
	if err != nil {
		t.Fatal(err)
	}
	if !panel.NeedsSync || !panel.Applied || !panel.CanRestore || panel.Status != "已汉化 · 待同步更新" {
		t.Fatalf("expected NeedsSync status, got NeedsSync=%v Applied=%v Status=%q", panel.NeedsSync, panel.Applied, panel.Status)
	}
	page, err := s.Find("agy", agyEditable.Key, "", "", 0)
	if err != nil || page.Count != 1 || page.Rows[0].Text != "自定义测试译文" || !page.Rows[0].Customized {
		t.Fatalf("unexpected Find after custom save: %+v, err=%v", page, err)
	}

	if err := s.SaveEntry("agy", agyEditable.Key, "", true, false); err != nil {
		t.Fatal(err)
	}
	page, _ = s.Find("agy", agyEditable.Key, "kept", "", 0)
	if page.Count != 1 || !page.Rows[0].Customized {
		t.Fatalf("expected kept customized entry, got %+v", page)
	}

	if err := s.SaveEntry("agy", agyEditable.Key, "", false, true); err != nil {
		t.Fatal(err)
	}
	page, _ = s.Find("agy", agyEditable.Key, "", "", 0)
	if page.Count != 1 || page.Rows[0].Customized || page.Rows[0].State != agyEditable.DefaultState {
		t.Fatalf("expected reset to restore default, got %+v", page)
	}

	// Restored install (agyArchiveHash) must NOT be marked refresh on dictionary edit.
	_ = os.Remove(filepath.Join(root, "agy", "refresh"))
	if err := jsonAtomic(filepath.Join(root, "agy", "identity.json"), Identity{Digests: map[string]string{agyArchive: agyArchiveHash}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEntry("devin", targetRow.Key, validCustom, false, false); err != nil {
		t.Fatalf("save devin entry with tokens: %v", err)
	}
	if err := s.SaveEntry("agy", agyEditable.Key, "原版下改词典", false, false); err != nil {
		t.Fatal(err)
	}
	panel, _ = s.Panel("agy")
	if panel.NeedsSync || panel.Applied || panel.CanRestore || panel.Status != "原版未汉化" {
		t.Fatalf("restored agy install should stay 原版未汉化 without refresh marker, got NeedsSync=%v Applied=%v Status=%q", panel.NeedsSync, panel.Applied, panel.Status)
	}
}

func TestInPlaceOriginalBackupAndProductIntegrity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALIZER_QA_ROOT", root)

	// Verify ensureOriginal snapshots live file into originals/ and survives after live file is modified in-place.
	agyTarget := targets["agy"]
	livePath := filepath.Join(targetOrigin(agyTarget), agyArchive)
	stockBytes := []byte("stock-asar-payload")
	stockHash := hash(stockBytes)
	if err := atomic(livePath, stockBytes); err != nil {
		t.Fatal(err)
	}
	got, err := ensureOriginal(root, agyTarget, agyArchive, stockHash)
	if err != nil || !bytes.Equal(got, stockBytes) {
		t.Fatalf("ensureOriginal initial snapshot failed: %v", err)
	}
	if err := atomic(livePath, []byte("patched-asar-payload")); err != nil {
		t.Fatal(err)
	}
	gotAfterPatch, err := ensureOriginal(root, agyTarget, agyArchive, stockHash)
	if err != nil || !bytes.Equal(gotAfterPatch, stockBytes) {
		t.Fatalf("ensureOriginal failed after live file was patched in-place: %v", err)
	}

	// Verify product.json checksum update preserves all unrelated top-level metadata fields.
	sampleProduct := []byte(`{
	"nameShort": "Devin",
	"applicationName": "devin",
	"dataFolderName": ".devin",
	"windsurfVersion": "3.14.30833",
	"checksums": {
		"vs/code/electron-browser/workbench/workbench.html": "oldA",
		"vs/sessions/electron-browser/sessions.html": "oldB",
		"vs/other.js": "keepMe"
	}
}`)
	var doc map[string]json.RawMessage
	if err := parseJSON(sampleProduct, &doc); err != nil {
		t.Fatal(err)
	}
	var checksums map[string]string
	if err := parseJSON(doc["checksums"], &checksums); err != nil {
		t.Fatal(err)
	}
	checksums["vs/code/electron-browser/workbench/workbench.html"] = base64.RawStdEncoding.EncodeToString([]byte("newA"))
	doc["checksums"] = jsonBytes(checksums)
	out, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	var verified struct {
		NameShort       string            `json:"nameShort"`
		ApplicationName string            `json:"applicationName"`
		DataFolderName  string            `json:"dataFolderName"`
		WindsurfVersion string            `json:"windsurfVersion"`
		Checksums       map[string]string `json:"checksums"`
	}
	if err := parseJSON(out, &verified); err != nil {
		t.Fatal(err)
	}
	if verified.WindsurfVersion != "3.14.30833" || verified.ApplicationName != "devin" || verified.DataFolderName != ".devin" || verified.Checksums["vs/other.js"] != "keepMe" {
		t.Fatalf("product.json metadata lost: %+v", verified)
	}
}

func TestCacheDoesNotRemoveUserDataOrBackups(t *testing.T) {
	root := t.TempDir()
	s := makeService(root)
	data := newCatalog(targets["agy"])
	if err := jsonAtomic(catalogPath(root, "agy"), data); err != nil {
		t.Fatal(err)
	}
	origBackup := filepath.Join(root, "agy", "originals", agyArchive)
	if err := atomic(origBackup, []byte("stock-backup")); err != nil {
		t.Fatal(err)
	}
	if err := atomic(filepath.Join(root, "cache", "devin-scan.json"), []byte("test")); err != nil {
		t.Fatal(err)
	}
	cleaned, err := s.Clean()
	if err != nil || cleaned != 4 {
		t.Fatalf("clean %d %v", cleaned, err)
	}
	if _, err := s.catalog(targets["agy"]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(catalogPath(root, "agy")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(origBackup); err != nil {
		t.Fatalf("Clean() must not remove originals backup: %v", err)
	}
}

func TestInterruptedWrite(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "agy")
	dir := filepath.Join(home, "install")
	dest := filepath.Join(dir, agyArchive)
	original, next := []byte("english"), []byte("chinese")
	if err := atomic(dest, original); err != nil {
		t.Fatal(err)
	}
	if err := atomic(filepath.Join(home, "next-0.tmp"), next); err != nil {
		t.Fatal(err)
	}
	pending := journal{[]transition{{agyArchive, hash(original), hash(next), false}}, Identity{map[string]string{agyArchive: hash(next)}}}
	if err := jsonAtomic(filepath.Join(home, "pending.json"), pending); err != nil {
		t.Fatal(err)
	}
	if err := atomic(dest, []byte("other")); err != nil {
		t.Fatal(err)
	}
	if err := recoverTransaction(home, dir, targets["agy"]); err == nil {
		t.Fatal("unexpected fingerprint accepted")
	}
	if err := atomic(dest, original); err != nil {
		t.Fatal(err)
	}
	if err := recoverTransaction(home, dir, targets["agy"]); err != nil {
		t.Fatal(err)
	}
	result, _ := dataFor(dest)
	if !bytes.Equal(result, next) {
		t.Fatal("pending write was not recovered")
	}
	if _, err := os.Stat(filepath.Join(home, "next-0.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary file was not cleaned")
	}
}

func TestSmartMistralDiscoveryAndWake(t *testing.T) {
	gotEP, err := localEndpoint("localhost:19820/v1/chat/completions")
	if err != nil || gotEP != "http://127.0.0.1:19820" {
		t.Fatalf("unexpected normalized endpoint: %q, %v", gotEP, err)
	}
	if _, err := localEndpoint("http://192.168.1.8:8080"); err == nil {
		t.Fatal("non-loopback endpoint must be rejected")
	}

	var engineRunning syncatomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"service":  "mistral-gui",
				"instance": "inst-unit-42",
			})
			return
		}
		if r.Header.Get("Authorization") != "Bearer unit-secret-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/status":
			st := "stopped"
			if engineRunning.Load() {
				st = "running"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":           map[string]any{"state": st},
				"settings":         map[string]any{"model": "HY-MT1.5-1.8B-Q4_K_M.gguf", "backend": "cuda"},
				"selected_backend": "cuda",
				"deployments": []map[string]any{
					{"id": "HY-MT1.5-1.8B-Q4_K_M.gguf", "model": "HY-MT1.5-1.8B-Q4_K_M.gguf", "status": map[string]any{"state": st}},
				},
			})
		case "/api/v1/runtime/start":
			engineRunning.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/v1/chat/completions":
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			prompt := ""
			if len(body.Messages) > 0 {
				prompt = body.Messages[0].Content
			}
			w.Header().Set("X-Request-ID", "req-unit-99")
			reply := "加载会话失败。"
			if strings.Contains(prompt, "[#0]") {
				reply = "同步 [#0] 的预算。"
			} else if strings.HasSuffix(prompt, "\n\nFlash Lite") {
				reply = "Flash Lite"
			} else if strings.HasSuffix(prompt, "\n\nopen settings") {
				reply = "open settings"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"finish_reason": "stop", "message": map[string]string{"content": reply}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mistralHome := t.TempDir()
	t.Setenv("LOCALIZER_MISTRAL_HOME", mistralHome)
	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := jsonAtomic(filepath.Join(mistralHome, "instance.json"), map[string]string{
		"addr":     addr,
		"instance": "inst-unit-42",
	}); err != nil {
		t.Fatal(err)
	}
	if err := jsonAtomic(filepath.Join(mistralHome, "state.json"), map[string]any{
		"config": map[string]any{
			"token":      "unit-secret-tok",
			"backend":    "cuda",
			"model":      "HY-MT1.5-1.8B-Q4_K_M.gguf",
			"auto_start": true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	probe, tok, _ := detectLocalModel(ctx, "")
	if !probe.Available || probe.Source != "mistral-gui" || probe.State != "standby" || probe.Backend != "CUDA" || probe.Model != "HY-MT1.5-1.8B-Q4_K_M.gguf" {
		t.Fatalf("unexpected auto-detected probe: %+v", probe)
	}
	if tok != "unit-secret-tok" || strings.Contains(string(jsonBytes(probe)), "unit-secret-tok") {
		t.Fatal("token mismatch or leaked into serialized ModelProbe")
	}

	var stages []string
	conn, stop, err := useModel(ctx, t.TempDir(), Pref{}, func(msg string) { stages = append(stages, msg) })
	if err != nil {
		t.Fatalf("useModel wake failed: %v", err)
	}
	defer stop()
	if len(stages) == 0 || !engineRunning.Load() {
		t.Fatalf("expected wake stage, got stages=%v running=%v", stages, engineRunning.Load())
	}
	translated, keep, reqID, err := translateOne(ctx, conn, "Failed to load session.")
	if err != nil || keep || reqID != "req-unit-99" || translated != "加载会话失败。" {
		t.Fatalf("translateOne via mistral-gui failed: %q, keep=%v, %q, %v", translated, keep, reqID, err)
	}

	shieldedText, keep2, _, err := translateOne(ctx, conn, "Sync budgets for {{accountId}}.")
	if err != nil || keep2 || shieldedText != "同步 {{accountId}} 的预算。" {
		t.Fatalf("placeholder shielding failed: %q, keep=%v, err=%v", shieldedText, keep2, err)
	}

	keptText, keep3, _, err := translateOne(ctx, conn, "Flash Lite")
	if err != nil || !keep3 || keptText != "" {
		t.Fatalf("expected proper noun Flash Lite to auto-keep, got text=%q keep=%v err=%v", keptText, keep3, err)
	}

	if _, keep4, _, err := translateOne(ctx, conn, "open settings"); err == nil || keep4 {
		t.Fatalf("lowercase untranslated phrase must not auto-keep: keep=%v err=%v", keep4, err)
	}

	for _, codeTerm := range []string{"MacOS", "rev27", "has_path_prefix(modified_files, ...)", "v{{version}}"} {
		if !retained(codeTerm) {
			t.Fatalf("expected %q to be retained as technical identifier", codeTerm)
		}
	}

	filtered := cleanDesktopEnv([]string{
		"PATH=C:\\Windows\\System32",
		"USERPROFILE=C:\\Users\\test",
		"ELECTRON_RUN_AS_NODE=1",
		"VSCODE_IPC_HOOK=\\\\.\\pipe\\sock",
		"WINDSURF_IDE_TYPE=windsurf",
		"DEVIN_FD_PATH=C:\\fd.exe",
		"NODE_OPTIONS=--inspect",
	})
	joined := strings.Join(filtered, "\n")
	if strings.Contains(joined, "ELECTRON_RUN_AS_NODE") || strings.Contains(joined, "VSCODE_") || strings.Contains(joined, "WINDSURF_") || strings.Contains(joined, "NODE_OPTIONS") {
		t.Fatalf("cleanDesktopEnv leaked host Electron/IDE vars: %v", filtered)
	}
	if !strings.Contains(joined, "PATH=") || !strings.Contains(joined, "USERPROFILE=") {
		t.Fatalf("cleanDesktopEnv dropped essential system vars: %v", filtered)
	}
}

func TestMistral(t *testing.T) {
	if os.Getenv("LOCALIZER_REAL_MISTRAL") != "1" {
		t.Skip("requires installed Mistral.rs and GGUF")
	}
	ctx := context.Background()
	conn, stop, err := useModel(ctx, t.TempDir(), Pref{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	value, _, id, err := translateOne(ctx, conn, "Failed to load session.")
	if err != nil || id == "" || !strings.Contains(value, "会话") {
		t.Fatalf("translation %q, %q, %v", value, id, err)
	}
	t.Log(value)
}

func TestGenerateAgySupplement(t *testing.T) {
	if os.Getenv("LOCALIZER_REAL_AGY_TRANSLATE") != "1" {
		t.Skip("requires installed Mistral.rs and GGUF")
	}
	root := t.TempDir()
	service := makeService(root)
	if err := service.Translate("agy", 0, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Minute)
	for {
		service.mu.Lock()
		job := service.job
		service.mu.Unlock()
		if !job.Running {
			if job.Success == 0 {
				t.Fatalf("translation job failed: %s", job.Message)
			}
			break
		}
		if time.Now().After(deadline) {
			service.Cancel()
			t.Fatal("translation job timed out")
		}
		time.Sleep(time.Second)
	}
	catalog, err := service.catalog(targets["agy"])
	if err != nil {
		t.Fatal(err)
	}
	supplement := map[string]string{}
	for _, row := range merge(service.inventories["agy"], catalog) {
		if row.State == "translated" {
			supplement[row.Source] = row.Text
		}
	}
	output, err := filepath.Abs("assets/agy-supplement.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonAtomic(output, supplement); err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d translations from %d scanned strings", len(supplement), len(service.inventories["agy"].rows))
}

func TestDevinIntegrityAndBundledLanguagePack(t *testing.T) {
	root := t.TempDir()
	page := []byte("original workbench")
	if err := os.WriteFile(filepath.Join(root, "workbench.html"), page, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(page)
	checksums := map[string]string{"workbench.html": base64.RawStdEncoding.EncodeToString(sum[:])}
	if err := verifyDevinChecksums(root, checksums); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workbench.html"), []byte("changed workbench"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyDevinChecksums(root, checksums); err == nil || !strings.Contains(err.Error(), "workbench.html") {
		t.Fatalf("changed page was not rejected: %v", err)
	}
	pack := asset("assets/" + devinPack)
	archive, err := zip.NewReader(bytes.NewReader(pack), int64(len(pack)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if strings.HasSuffix(file.Name, ".js") || strings.HasSuffix(file.Name, ".exe") {
			t.Fatalf("unexpected executable in the language pack: %s", file.Name)
		}
	}
	inv := makeInventory("devin", nil)
	if inv.byKey["settings.linear"].State != "kept" || inv.byKey["settings.modelGroupGemini"].State != "kept" || inv.byKey["activity.kind.shell"].Text != "终端命令" {
		t.Fatal("bundled catalog lost reviewed terms")
	}
	if canAutoKeepUnchanged("Pin Tab", "Pin Tab") {
		t.Fatal("an untranslated title-cased action was marked as a proper name")
	}
}
