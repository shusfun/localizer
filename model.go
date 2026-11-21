package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var localHTTP = &http.Client{
	Timeout:   55 * time.Second,
	Transport: &http.Transport{},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("本地接口重定向被拒绝")
	},
}

type ModelProbe struct {
	Available bool
	Source    string
	State     string
	Label     string
	Endpoint  string
	Model     string
	Backend   string
	Models    []string
	Detail    string
}

type modelConn struct {
	endpoint      string
	token         string
	model         string
	viaMistralGUI bool
}

type mistralLocalInstall struct {
	baseDir       string
	instanceAddr  string
	instanceID    string
	token         string
	configModel   string
	backend       string
	autoStart     bool
	enginePath    string
	modelPath     string
	installedGGUF []string
}

func localEndpoint(input string) (string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", errors.New("请输入本地 HTTP 地址，例如 http://127.0.0.1:19820")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	value, err := url.Parse(raw)
	if err != nil || value.Scheme != "http" || value.User != nil || value.RawQuery != "" || value.Fragment != "" || value.Port() == "" {
		return "", errors.New("请输入带端口的本地 HTTP 地址，例如 http://127.0.0.1:19820")
	}
	host := strings.ToLower(value.Hostname())
	if host == "localhost" {
		value.Host = "127.0.0.1:" + value.Port()
	} else {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("只允许连接本机回环地址（127.0.0.1 / localhost）")
		}
	}
	cleanPath := strings.TrimRight(value.Path, "/")
	for _, suffix := range []string{"/v1/chat/completions", "/v1/models", "/api/v1/translate", "/api/v1/status", "/v1"} {
		if strings.HasSuffix(strings.ToLower(cleanPath), suffix) {
			cleanPath = cleanPath[:len(cleanPath)-len(suffix)]
			break
		}
	}
	value.Path = cleanPath
	return value.String(), nil
}

func mistralDataRoot() string {
	if override := os.Getenv("LOCALIZER_MISTRAL_HOME"); override != "" {
		return override
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "mistral-gui")
	}
	return ""
}

func readMistralInstall() mistralLocalInstall {
	var out mistralLocalInstall
	base := mistralDataRoot()
	if base == "" {
		return out
	}
	out.baseDir = base

	var inst struct {
		Addr     string `json:"addr"`
		Instance string `json:"instance"`
	}
	if readJSON(filepath.Join(base, "instance.json"), &inst) == nil && inst.Addr != "" {
		if ep, err := localEndpoint(inst.Addr); err == nil {
			out.instanceAddr = ep
			out.instanceID = inst.Instance
		}
	}

	type modelAsset struct {
		ID, Kind, Format, Path, Backend string
	}
	var state struct {
		Config struct {
			Token     string `json:"token"`
			Backend   string `json:"backend"`
			Model     string `json:"model"`
			Engine    string `json:"engine"`
			AutoStart bool   `json:"auto_start"`
		} `json:"config"`
		Installed []modelAsset `json:"installed"`
	}
	if readJSON(filepath.Join(base, "state.json"), &state) != nil {
		return out
	}

	if tok, err := unprotectLocalSecret(state.Config.Token); err == nil {
		out.token = tok
	}
	out.configModel = strings.TrimSpace(state.Config.Model)
	out.backend = strings.ToUpper(strings.TrimSpace(state.Config.Backend))
	out.autoStart = state.Config.AutoStart

	withinBase := func(p string) bool {
		if p == "" || !filepath.IsAbs(p) {
			return false
		}
		rel, err := filepath.Rel(base, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return false
		}
		_, err = os.Stat(p)
		return err == nil
	}

	if withinBase(state.Config.Engine) && strings.EqualFold(filepath.Base(state.Config.Engine), "mistralrs.exe") {
		out.enginePath = state.Config.Engine
	}

	var fallbackModelPath string
	seenModels := map[string]bool{}
	for _, entry := range state.Installed {
		if !withinBase(entry.Path) {
			continue
		}
		if out.enginePath == "" && entry.Kind == "engine" && strings.EqualFold(filepath.Base(entry.Path), "mistralrs.exe") {
			out.enginePath = entry.Path
			if out.backend == "" || out.backend == "AUTO" {
				out.backend = strings.ToUpper(entry.Backend)
			}
		}
		if entry.Format == "gguf" && strings.EqualFold(filepath.Ext(entry.Path), ".gguf") {
			id := entry.ID
			if id == "" {
				id = filepath.Base(entry.Path)
			}
			if !seenModels[id] {
				seenModels[id] = true
				out.installedGGUF = append(out.installedGGUF, id)
			}
			if out.configModel != "" && (strings.EqualFold(id, out.configModel) || strings.EqualFold(filepath.Base(entry.Path), out.configModel)) {
				out.modelPath = entry.Path
			}
			if fallbackModelPath == "" {
				fallbackModelPath = entry.Path
				if out.configModel == "" {
					out.configModel = id
				}
			}
		}
	}
	if out.modelPath == "" {
		out.modelPath = fallbackModelPath
	}
	return out
}

func shortHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     localHTTP.Transport,
		CheckRedirect: localHTTP.CheckRedirect,
	}
}

func probeMistralManager(ctx context.Context, endpoint string, info mistralLocalInstall, verifyInstance bool) (ModelProbe, string, bool) {
	client := shortHTTPClient(900 * time.Millisecond)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/healthz", nil)
	if err != nil {
		return ModelProbe{}, "", false
	}
	res, err := client.Do(req)
	if err != nil {
		return ModelProbe{}, "", false
	}
	var health struct {
		OK       bool   `json:"ok"`
		Service  string `json:"service"`
		Instance string `json:"instance"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&health)
	res.Body.Close()
	if res.StatusCode != 200 || decodeErr != nil || health.Service != "mistral-gui" || !health.OK {
		return ModelProbe{}, "", false
	}
	if verifyInstance && info.instanceID != "" && health.Instance != "" && health.Instance != info.instanceID {
		return ModelProbe{}, "", false
	}
	if info.token == "" {
		return ModelProbe{
			Available: false,
			Source:    "mistral-gui",
			State:     "auth-missing",
			Label:     "检测到 Mistral 服务，但缺少访问令牌",
			Endpoint:  endpoint,
			Detail:    "请打开一次 Mistral 桌面端完成初始化后再试",
		}, "", true
	}

	statusReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/v1/status", nil)
	if err != nil {
		return ModelProbe{}, "", false
	}
	statusReq.Header.Set("Authorization", "Bearer "+info.token)
	statusRes, err := client.Do(statusReq)
	if err != nil {
		return ModelProbe{}, "", false
	}
	defer statusRes.Body.Close()
	if statusRes.StatusCode != 200 {
		return ModelProbe{
			Available: false,
			Source:    "mistral-gui",
			State:     "auth-rejected",
			Label:     "Mistral 服务令牌未通过校验",
			Endpoint:  endpoint,
			Detail:    fmt.Sprintf("状态查询返回 HTTP %d", statusRes.StatusCode),
		}, "", true
	}

	var payload struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
		Settings struct {
			Model   string `json:"model"`
			Backend string `json:"backend"`
		} `json:"settings"`
		SelectedBackend string `json:"selected_backend"`
		Deployments     []struct {
			ID     string `json:"id"`
			Model  string `json:"model"`
			Status struct {
				State string `json:"state"`
			} `json:"status"`
		} `json:"deployments"`
	}
	if err := json.NewDecoder(io.LimitReader(statusRes.Body, 512<<10)).Decode(&payload); err != nil {
		return ModelProbe{}, "", false
	}

	modelName := strings.TrimSpace(payload.Settings.Model)
	if modelName == "" {
		modelName = info.configModel
	}
	backend := strings.ToUpper(strings.TrimSpace(payload.SelectedBackend))
	if backend == "" {
		backend = strings.ToUpper(strings.TrimSpace(payload.Settings.Backend))
	}

	seen := map[string]bool{}
	var models []string
	addModel := func(m string) {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			seen[m] = true
			models = append(models, m)
		}
	}
	addModel(modelName)
	runningState := payload.Status.State == "running"
	for _, d := range payload.Deployments {
		addModel(d.ID)
		addModel(d.Model)
		if (d.ID == modelName || d.Model == modelName) && d.Status.State == "running" {
			runningState = true
		}
	}
	for _, m := range info.installedGGUF {
		addModel(m)
	}

	if modelName == "" && len(models) == 0 {
		return ModelProbe{
			Available: false,
			Source:    "mistral-gui",
			State:     "no-model",
			Label:     "Mistral 服务未安装本地模型",
			Endpoint:  endpoint,
			Backend:   backend,
			Detail:    "请先在 Mistral 桌面端下载 GGUF 翻译模型",
		}, info.token, true
	}

	stateKey := "standby"
	label := "Mistral 桌面服务 · 空闲自动唤醒"
	if runningState {
		stateKey = "running"
		label = "Mistral 桌面服务 · 运行中"
	}
	hostPort := strings.TrimPrefix(endpoint, "http://")
	detailParts := []string{hostPort}
	if modelName != "" {
		detailParts = append(detailParts, modelName)
	}
	return ModelProbe{
		Available: true,
		Source:    "mistral-gui",
		State:     stateKey,
		Label:     label,
		Endpoint:  endpoint,
		Model:     modelName,
		Backend:   backend,
		Models:    models,
		Detail:    strings.Join(detailParts, " · "),
	}, info.token, true
}

func probeOpenAICompatible(ctx context.Context, endpoint string) (ModelProbe, bool) {
	client := shortHTTPClient(700 * time.Millisecond)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/models", nil)
	if err != nil {
		return ModelProbe{}, false
	}
	res, err := client.Do(req)
	if err != nil {
		return ModelProbe{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return ModelProbe{}, false
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&list)
	var models []string
	seen := map[string]bool{}
	for _, item := range list.Data {
		id := strings.TrimSpace(item.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	for _, item := range list.Models {
		id := strings.TrimSpace(item.Name)
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	primary := "default"
	if len(models) > 0 {
		primary = models[0]
	}
	hostPort := strings.TrimPrefix(endpoint, "http://")
	detail := hostPort
	if primary != "" && primary != "default" {
		detail += " · " + primary
	}
	return ModelProbe{
		Available: true,
		Source:    "openai-local",
		State:     "running",
		Label:     "本地推理接口 · 已连接",
		Endpoint:  endpoint,
		Model:     primary,
		Models:    models,
		Detail:    detail,
	}, true
}

func detectLocalModel(ctx context.Context, customEndpoint string) (ModelProbe, string, mistralLocalInstall) {
	info := readMistralInstall()

	if strings.TrimSpace(customEndpoint) != "" {
		ep, err := localEndpoint(customEndpoint)
		if err != nil {
			return ModelProbe{
				Available: false,
				Source:    "none",
				State:     "invalid",
				Label:     "接口地址格式无效",
				Detail:    err.Error(),
			}, "", info
		}
		verifyInst := info.instanceAddr != "" && ep == info.instanceAddr
		if probe, tok, matched := probeMistralManager(ctx, ep, info, verifyInst); matched {
			return probe, tok, info
		}
		if probe, ok := probeOpenAICompatible(ctx, ep); ok {
			return probe, "", info
		}
		return ModelProbe{
			Available: false,
			Source:    "none",
			State:     "unreachable",
			Label:     "无法连接指定本地接口",
			Endpoint:  ep,
			Detail:    fmt.Sprintf("%s 未响应 /healthz 或 /v1/models", strings.TrimPrefix(ep, "http://")),
		}, "", info
	}

	if info.instanceAddr != "" {
		if probe, tok, matched := probeMistralManager(ctx, info.instanceAddr, info, true); matched && probe.Available {
			return probe, tok, info
		}
	}

	candidatePorts := []string{"19820", "8080", "1234", "8000", "11434"}
	type scanHit struct {
		rank  int
		probe ModelProbe
		token string
	}
	hits := make(chan scanHit, len(candidatePorts))
	scanCtx, cancel := context.WithTimeout(ctx, 550*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	for idx, port := range candidatePorts {
		ep := "http://127.0.0.1:" + port
		if ep == info.instanceAddr {
			continue
		}
		wg.Add(1)
		go func(rank int, targetEP string) {
			defer wg.Done()
			if probe, tok, matched := probeMistralManager(scanCtx, targetEP, info, false); matched && probe.Available {
				hits <- scanHit{rank: rank, probe: probe, token: tok}
				return
			}
			if probe, ok := probeOpenAICompatible(scanCtx, targetEP); ok {
				hits <- scanHit{rank: rank + 10, probe: probe}
			}
		}(idx, ep)
	}
	wg.Wait()
	close(hits)

	bestRank := 999
	var best scanHit
	for h := range hits {
		if h.rank < bestRank {
			bestRank = h.rank
			best = h
		}
	}
	if bestRank < 999 {
		return best.probe, best.token, info
	}

	if info.enginePath != "" && info.modelPath != "" {
		modelLabel := info.configModel
		if modelLabel == "" {
			modelLabel = filepath.Base(info.modelPath)
		}
		return ModelProbe{
			Available: true,
			Source:    "offline-engine",
			State:     "offline-ready",
			Label:     "本机 Mistral.rs 引擎 · 按需启动",
			Model:     modelLabel,
			Backend:   info.backend,
			Models:    info.installedGGUF,
			Detail:    modelLabel + "（开始翻译时自动拉起临时推理进程）",
		}, "", info
	}

	return ModelProbe{
		Available: false,
		Source:    "none",
		State:     "unavailable",
		Label:     "未检测到本机 Mistral 服务",
		Detail:    "请先安装并启动 Mistral 桌面端，或在下方手动填写本地 HTTP 接口",
	}, "", info
}

func wakeMistralManager(ctx context.Context, endpoint, token, targetModel string) error {
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/v1/runtime/start", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	startReq.Header.Set("Authorization", "Bearer "+token)
	startReq.Header.Set("Content-Type", "application/json")
	client := shortHTTPClient(5 * time.Second)
	if res, err := client.Do(startReq); err == nil {
		res.Body.Close()
	}

	until := time.Now().Add(65 * time.Second)
	pollClient := shortHTTPClient(1500 * time.Millisecond)
	statusURL := endpoint + "/api/v1/status"
	if targetModel != "" && targetModel != "default" {
		statusURL += "?model=" + url.QueryEscape(targetModel)
	}
	for time.Now().Before(until) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := pollClient.Do(req)
		if e == nil {
			var st struct {
				Status struct {
					State string `json:"state"`
					Error string `json:"error"`
				} `json:"status"`
			}
			_ = json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(&st)
			res.Body.Close()
			if st.Status.State == "running" {
				return nil
			}
			if st.Status.State == "failed" || st.Status.State == "cooldown" {
				if st.Status.Error != "" {
					return fmt.Errorf("Mistral 推理引擎启动失败：%s", st.Status.Error)
				}
				return errors.New("Mistral 推理引擎启动失败")
			}
		}
		time.Sleep(350 * time.Millisecond)
	}
	return errors.New("等待 Mistral 推理引擎就绪超时")
}

func useModel(ctx context.Context, root string, pref Pref, onStage func(string)) (modelConn, func(), error) {
	probe, token, info := detectLocalModel(ctx, pref.Endpoint)
	chosenModel := strings.TrimSpace(pref.Model)
	if chosenModel == "" {
		chosenModel = probe.Model
	}
	if chosenModel == "" {
		chosenModel = "default"
	}

	if probe.Available && probe.Source == "mistral-gui" {
		if probe.State != "running" {
			if onStage != nil {
				onStage("正在唤醒 Mistral 推理引擎")
			}
			if err := wakeMistralManager(ctx, probe.Endpoint, token, chosenModel); err != nil {
				return modelConn{}, nil, err
			}
		}
		return modelConn{
			endpoint:      probe.Endpoint,
			token:         token,
			model:         chosenModel,
			viaMistralGUI: true,
		}, func() {}, nil
	}

	if probe.Available && probe.Source == "openai-local" {
		return modelConn{
			endpoint: probe.Endpoint,
			model:    chosenModel,
		}, func() {}, nil
	}

	if strings.TrimSpace(pref.Endpoint) != "" {
		return modelConn{}, nil, errors.New(probe.Detail)
	}

	if info.enginePath == "" || info.modelPath == "" {
		return modelConn{}, nil, errors.New("未检测到运行中的本地接口，也未找到已安装的 Mistral.rs 与 GGUF 模型")
	}

	if onStage != nil {
		onStage("正在拉起本机 Mistral.rs 引擎")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return modelConn{}, nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := hidden(info.enginePath, "serve", "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--no-ui", "--token-source", "none", "--max-model-len", "2048", "--max-seqs", "8", "--quantized-file", info.modelPath)
	cmd.Env = append(os.Environ(), "HF_HUB_OFFLINE=1")
	cmd.Dir = root
	if err = cmd.Start(); err != nil {
		return modelConn{}, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	stop := func() { _ = cmd.Process.Kill(); <-done }
	until := time.Now().Add(40 * time.Second)
	short := shortHTTPClient(1200 * time.Millisecond)
	for time.Now().Before(until) {
		select {
		case err := <-done:
			return modelConn{}, nil, fmt.Errorf("本机推理服务启动失败：%v", err)
		case <-ctx.Done():
			stop()
			return modelConn{}, nil, ctx.Err()
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/models", nil)
		res, e := short.Do(req)
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return modelConn{endpoint: endpoint, model: "default"}, stop, nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	stop()
	return modelConn{}, nil, errors.New("等待本机推理服务超时")
}

var shieldTokenRe = regexp.MustCompile(`\{\{[^{}]+\}\}|\$\{[^{}]+\}|\{[0-9]+\}|%[sdif]`)
var hunyuanModelRe = regexp.MustCompile(`(?i)(?:^|/|[-_])(?:hy-mt|hunyuan)`)

func cleanModelOutput(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<think>") {
		if idx := strings.Index(trimmed, "</think>"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[idx+len("</think>"):])
		}
	}
	if strings.HasPrefix(trimmed, "```") && strings.HasSuffix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 3 {
			trimmed = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	return trimmed
}

func shieldPlaceholders(source string) (string, []string) {
	if strings.Contains(source, "[#") {
		return source, nil
	}
	var slots []string
	masked := shieldTokenRe.ReplaceAllStringFunc(source, func(match string) string {
		idx := len(slots)
		slots = append(slots, match)
		return fmt.Sprintf("[#%d]", idx)
	})
	return masked, slots
}

func unshieldPlaceholders(text string, slots []string) string {
	out := text
	for i, orig := range slots {
		for _, variant := range []string{
			fmt.Sprintf("[#%d]", i),
			fmt.Sprintf("［#%d］", i),
			fmt.Sprintf("[# %d]", i),
			fmt.Sprintf("[ #%d ]", i),
		} {
			out = strings.ReplaceAll(out, variant, orig)
		}
	}
	return out
}

func buildTranslatePrompt(model, input string) string {
	if hunyuanModelRe.MatchString(model) {
		return "Translate the following segment into Chinese, without additional explanation.\n\n" + input
	}
	return "Translate the following UI text into Simplified Chinese. Output only the translation without explanation, keeping all placeholders and tags intact.\n\n" + input
}

func canAutoKeepUnchanged(source, reply string) bool {
	norm := func(s string) string {
		return strings.TrimSpace(strings.ReplaceAll(s, "…", "..."))
	}
	src := norm(source)
	rep := norm(reply)
	if src == "" || !strings.EqualFold(src, rep) || strings.Contains(src, "\n") || len(src) > 56 {
		return false
	}
	if strings.HasSuffix(src, "?") || strings.HasSuffix(src, "!") || (strings.HasSuffix(src, ".") && !strings.HasSuffix(src, "...")) {
		return false
	}
	stripped := strings.TrimSpace(tokenPattern.ReplaceAllString(src, " "))
	words := strings.Fields(stripped)
	if len(words) == 0 || len(words) > 4 {
		return len(words) == 0
	}
	for _, w := range words {
		clean := strings.Trim(w, "()[]{}.,:;/-_+#*'\"“”")
		if clean == "" {
			continue
		}
		hasDigitOrSymbol := strings.ContainsAny(clean, "0123456789_/+.#")
		firstRune := []rune(clean)[0]
		if !unicode.IsUpper(firstRune) && !hasDigitOrSymbol {
			return false
		}
	}
	return true
}

func callModelLine(ctx context.Context, conn modelConn, model, line string) (string, string, error) {
	maskedSource, slots := shieldPlaceholders(line)
	prompt := buildTranslatePrompt(model, maskedSource)
	body := jsonBytes(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.1,
		"max_tokens":  min(1024, max(160, len(line)*2)),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if conn.token != "" {
		req.Header.Set("Authorization", "Bearer "+conn.token)
	}
	res, err := localHTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	id := res.Header.Get("X-Request-ID")
	if id == "" {
		id = res.Header.Get("x-request-id")
	}
	if res.StatusCode != 200 {
		return "", id, fmt.Errorf("本地模型返回 HTTP %d", res.StatusCode)
	}
	var result struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result); err != nil {
		return "", id, err
	}
	if len(result.Choices) == 0 || result.Choices[0].Finish == "length" {
		return "", id, errors.New("翻译返回不完整")
	}
	rawOut := strings.ReplaceAll(cleanModelOutput(result.Choices[0].Message.Content), "\n", " ")
	return unshieldPlaceholders(strings.TrimSpace(rawOut), slots), id, nil
}

func translateOne(ctx context.Context, conn modelConn, source string) (string, bool, string, error) {
	model := conn.model
	if model == "" {
		model = "default"
	}
	var text, lastID string
	if strings.Contains(source, "\n") {
		parts := strings.Split(source, "\n")
		outParts := make([]string, len(parts))
		for i, part := range parts {
			if strings.TrimSpace(part) == "" {
				outParts[i] = part
				continue
			}
			trLine, reqID, err := callModelLine(ctx, conn, model, part)
			if reqID != "" {
				lastID = reqID
			}
			if err != nil {
				return "", false, lastID, err
			}
			outParts[i] = trLine
		}
		text = strings.Join(outParts, "\n")
	} else {
		var err error
		text, lastID, err = callModelLine(ctx, conn, model, source)
		if err != nil {
			return "", false, lastID, err
		}
	}
	if canAutoKeepUnchanged(source, text) {
		return "", true, lastID, nil
	}
	if !han.MatchString(text) || !validText(source, text) || strings.Contains(text, "翻译以下") || strings.Contains(text, "无需额外解释") {
		return "", false, lastID, errors.New("译文未通过格式校验")
	}
	return text, false, lastID, nil
}

type modelResult struct {
	Key, Text, Request string
	Keep               bool
	Err                error
}

func translateBatch(ctx context.Context, conn modelConn, rows []Row, handle func(modelResult)) {
	jobs := make(chan Row)
	results := make(chan modelResult)
	var pool sync.WaitGroup
	for i := 0; i < min(8, len(rows)); i++ {
		pool.Add(1)
		go func() {
			defer pool.Done()
			for row := range jobs {
				text, keep, id, err := translateOne(ctx, conn, row.Source)
				results <- modelResult{Key: row.Key, Text: text, Request: id, Keep: keep, Err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, row := range rows {
			select {
			case jobs <- row:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { pool.Wait(); close(results) }()
	for result := range results {
		handle(result)
	}
}
