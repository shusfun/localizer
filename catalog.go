package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed assets ui
var resources embed.FS

type Row struct {
	Key, Group, Source, Text, State string
	DefaultState                    string
	Customized                      bool
}
type Catalog struct {
	Schema   int              `json:"schema"`
	App      string           `json:"app"`
	Version  string           `json:"version"`
	Locale   string           `json:"locale"`
	Name     string           `json:"name"`
	Messages map[string]Entry `json:"messages"`
}
type Entry struct {
	Source string `json:"source"`
	Text   string `json:"text"`
	Keep   bool   `json:"keep,omitempty"`
}
type Summary struct {
	Name                      string
	Translated, Missing, Kept int
}
type inventory struct {
	rows  []Row
	byKey map[string]Row
}

func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func asset(name string) []byte {
	data, err := resources.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}
func parseJSON(data []byte, value any) error {
	return json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), value)
}
func jsonBytes(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
func readJSON(path string, value any) error {
	data, err := readBounded(path, 24<<20)
	if err != nil {
		return err
	}
	return parseJSON(data, value)
}
func readBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err == nil && int64(len(data)) > maximum {
		return nil, errors.New("文件大小超出限制")
	}
	return data, err
}
func agyKey(source string) string { return "ui." + hash([]byte(source))[:20] }

var retainPattern = regexp.MustCompile("^(?:[A-Z][A-Z0-9 _+./#-]{0,30}|[0-9 .:/%-]+|https?://[^ ]+|[A-Za-z0-9_]+[./][A-Za-z0-9_./]+|[A-Za-z0-9_]+(?:_[A-Za-z0-9_]+)+|[A-Za-z_][A-Za-z0-9_.]*\\([^()]*\\)|(?:rev|v)[0-9]+(?:\\.[0-9]+)*)$")
var retainNamed = map[string]bool{"oauth": true, "devin": true, "devin local": true, "devin cloud": true, "devin cli": true, "github": true, "google": true, "google workspace": true, "linux": true, "windows": true, "macos": true, "antigravity": true, "mistral": true, "inter": true, "noto sans sc": true, "docker": true, "markdown": true, "kubernetes": true, "flask": true, "flash lite": true, "cog (gob)": true, "elk box": true}
var latin = regexp.MustCompile("[A-Za-z]")
var han = regexp.MustCompile("[\\p{Han}]")
var tokenPattern = regexp.MustCompile("(?s)\x60[^\x60]*\x60|<code(?:\\s[^>]*)?>.*?</code>|\\{\\{[^{}]+\\}\\}|\\$\\{[^{}]+\\}|\\{[0-9]+\\}|https?://(?:[A-Za-z0-9._~:/?#\\[\\]@!$&'*+,;=%-]*[A-Za-z0-9/#%=_-])?|%[sdif]|</?[A-Za-z][^<>]*>|\\]\\([^()\\s]*\\)|(?:[A-Za-z]:[\\\\/]|~/|\\./)(?:[A-Za-z0-9._\\\\/-]*[A-Za-z0-9_\\\\/-])?|--[a-zA-Z][a-zA-Z0-9_-]*")

func retained(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || retainNamed[strings.ToLower(text)] || retainPattern.MatchString(text) {
		return true
	}
	stripped := strings.TrimSpace(tokenPattern.ReplaceAllString(text, ""))
	return !latin.MatchString(stripped) || stripped == "v"
}
func isOrderedToken(tok string) bool {
	if len(tok) == 2 && tok[0] == '%' {
		return true
	}
	if strings.HasPrefix(tok, "<") && !(strings.HasPrefix(tok, "<code") && strings.HasSuffix(tok, "</code>")) {
		return true
	}
	return false
}
func splitTokens(input string) (ordered, unordered []string) {
	for _, tok := range tokenPattern.FindAllString(input, -1) {
		if isOrderedToken(tok) {
			ordered = append(ordered, tok)
		} else {
			unordered = append(unordered, tok)
		}
	}
	sort.Strings(unordered)
	return ordered, unordered
}
func validateEntryText(source, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("译文不能为空；如需保留英文原文请切换「保留原文」")
	}
	if strings.ContainsRune(text, 0) || len(text) > max(180, len(source)*4) {
		return errors.New("译文长度超出允许范围")
	}
	if want, got := strings.Count(source, "\n"), strings.Count(text, "\n"); want != got {
		return fmt.Errorf("换行数量需与原文一致（原文 %d 处，当前 %d 处）", want, got)
	}
	ordA, unordA := splitTokens(source)
	ordB, unordB := splitTokens(text)
	if len(ordA) != len(ordB) {
		return errors.New("标签或格式化占位符数量与原文不一致")
	}
	for i := range ordA {
		if ordA[i] != ordB[i] {
			return fmt.Errorf("标签或占位符顺序不匹配：应保留 %s", ordA[i])
		}
	}
	counts := map[string]int{}
	for _, tok := range unordA {
		counts[tok]++
	}
	for _, tok := range unordB {
		counts[tok]--
	}
	for _, tok := range unordA {
		if counts[tok] > 0 {
			return fmt.Errorf("缺少原文中的受保护片段：%s", tok)
		}
	}
	for _, tok := range unordB {
		if counts[tok] < 0 {
			return fmt.Errorf("包含原文未出现的受保护片段：%s", tok)
		}
	}
	return nil
}
func validText(source, text string) bool {
	return validateEntryText(source, text) == nil
}

func makeInventory(id string, extra map[string]string) inventory {
	inv := inventory{byKey: map[string]Row{}}
	add := func(key, group, source, text string) {
		state := "missing"
		switch {
		case retained(source):
			state = "kept"
			text = ""
		case text != "" && text != source && validText(source, text):
			state = "translated"
		default:
			text = ""
		}
		row := Row{Key: key, Group: group, Source: source, Text: text, State: state, DefaultState: state}
		inv.rows = append(inv.rows, row)
		inv.byKey[key] = row
	}
	if id == "devin" {
		var en struct{ Namespaces map[string]map[string]string }
		var zh map[string]map[string]string
		var supplement map[string]string
		_ = parseJSON(asset("assets/devin-en.json"), &en)
		_ = parseJSON(asset("assets/devin-zh.json"), &zh)
		_ = parseJSON(asset("assets/devin-supplement.json"), &supplement)
		for group, table := range en.Namespaces {
			for key, source := range table {
				text := zh[group][key]
				if (text == "" || text == source) && supplement[source] != "" {
					text = supplement[source]
				}
				add(group+"."+key, group, source, text)
			}
		}
	} else {
		var en map[string]string
		_ = parseJSON(asset("assets/agy-en.json"), &en)
		for text, group := range extra {
			if en[text] == "" {
				en[text] = group
			}
		}
		defaults := map[string]string{}
		// Plain product-independent labels can reuse the reviewed Devin catalog.
		var devinSource struct{ Namespaces map[string]map[string]string }
		var devinChinese map[string]map[string]string
		_ = parseJSON(asset("assets/devin-en.json"), &devinSource)
		_ = parseJSON(asset("assets/devin-zh.json"), &devinChinese)
		conflicts := map[string]bool{}
		for group, entries := range devinSource.Namespaces {
			for key, english := range entries {
				if en[english] == "" {
					continue
				}
				translation := devinChinese[group][key]
				if !han.MatchString(translation) || !validText(english, translation) {
					continue
				}
				if previous := defaults[english]; previous != "" && previous != translation {
					conflicts[english] = true
				}
				defaults[english] = translation
			}
		}
		for english := range conflicts {
			delete(defaults, english)
		}
		for _, line := range strings.Split(strings.TrimPrefix(string(asset("assets/agy-zh.txt")), "\ufeff"), "\n") {
			source, text, ok := strings.Cut(strings.TrimSpace(line), "|")
			if !ok {
				continue
			}
			defaults[source] = text
			if en[source] == "" {
				en[source] = "界面常用语"
			}
		}
		var supplement map[string]string
		_ = parseJSON(asset("assets/agy-supplement.json"), &supplement)
		for source, text := range supplement {
			defaults[source] = text
			if en[source] == "" {
				en[source] = "界面常用语"
			}
		}
		for source, group := range en {
			add(agyKey(source), group, source, defaults[source])
		}
	}
	sort.Slice(inv.rows, func(i, j int) bool { return inv.rows[i].Key < inv.rows[j].Key })
	return inv
}
func newCatalog(t target) Catalog {
	return Catalog{1, t.id, t.version, "zh-CN", "简体中文", map[string]Entry{}}
}
func validateCatalog(c Catalog, t target, inv inventory) error {
	if c.Schema != 1 || c.App != t.id || c.Version != t.version || c.Locale != "zh-CN" || strings.TrimSpace(c.Name) == "" || len(c.Name) > 80 || c.Messages == nil {
		return errors.New("词典格式、应用或来源版本不匹配")
	}
	var invalid []string
	for key, value := range c.Messages {
		row, ok := inv.byKey[key]
		if !ok || row.Source != value.Source || (!value.Keep && value.Text != "" && (row.State == "kept" || !validText(row.Source, value.Text))) {
			invalid = append(invalid, key)
		}
	}
	sort.Strings(invalid)
	if len(invalid) > 0 {
		return fmt.Errorf("%d 个词条不匹配或破坏了原文格式：%s", len(invalid), strings.Join(invalid[:min(len(invalid), 10)], "、"))
	}
	return nil
}
func merge(inv inventory, c Catalog) []Row {
	rows := append([]Row(nil), inv.rows...)
	for i := range rows {
		record, ok := c.Messages[rows[i].Key]
		if !ok {
			continue
		}
		if record.Keep {
			rows[i].Customized = rows[i].DefaultState != "kept"
			rows[i].State = "kept"
			rows[i].Text = ""
			continue
		}
		if record.Text != "" {
			rows[i].Customized = rows[i].DefaultState != "translated" || rows[i].Text != record.Text
			rows[i].State = "translated"
			rows[i].Text = record.Text
		}
	}
	return rows
}
func exportCatalog(rows []Row, c Catalog) Catalog {
	c.Messages = map[string]Entry{}
	for _, row := range rows {
		if row.State == "missing" {
			continue
		}
		c.Messages[row.Key] = Entry{row.Source, row.Text, row.State == "kept"}
	}
	return c
}
func aggregate(rows []Row) ([]Summary, Summary) {
	total := Summary{Name: "全部"}
	groups := map[string]*Summary{}
	for _, row := range rows {
		group := displayGroup(row.Group)
		if groups[group] == nil {
			groups[group] = &Summary{Name: group}
		}
		for _, sum := range []*Summary{&total, groups[group]} {
			switch row.State {
			case "translated":
				sum.Translated++
			case "kept":
				sum.Kept++
			default:
				sum.Missing++
			}
		}
	}
	list := make([]Summary, 0, len(groups))
	for _, g := range groups {
		list = append(list, *g)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Missing > list[j].Missing || list[i].Missing == list[j].Missing && list[i].Name < list[j].Name
	})
	return list, total
}
func displayGroup(name string) string {
	if v := map[string]string{"label": "按钮与标签", "title": "标题", "placeholder": "输入提示", "ariaLabel": "辅助标签", "tooltip": "悬停提示", "buttonText": "按钮", "settings": "设置", "settingsPages": "设置页面", "settingsHub": "设置中心", "sidebar": "侧边栏", "transcript": "会话记录", "session": "会话", "worklog": "工作记录", "progress": "进度", "sessionMenu": "会话菜单", "sessionStatus": "会话状态", "common": "通用"}[name]; v != "" {
		return v
	}
	return name
}
func catalogPath(root, id string) string { return filepath.Join(root, id, "dictionary.json") }
