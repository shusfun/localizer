package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const devinProduct = "resources/app/product.json"
const devinAssets = "resources/app/out/vs/workbench/devin-webapp-host/assets/"
const devinEntry = devinAssets + "chunk-entry-host-editor-mode-menu-2k2EH-7U.js"
const devinData = devinAssets + "local-locale-zh-CN.js"
const agyArchive = "resources/app.asar"
const devinEntryHash = "c0af92a728058fb4fef66bbdef2b0c8edc0b32ef94c2a300cd439f638ac40155"
const devinProductHash = "5209e7fafcc82fbad40930743e3e08014d2b2b2982672f98fc9c8625beff0c8d"
const devinWorkbenchHTML = "resources/app/out/vs/code/electron-browser/workbench/workbench.html"
const devinSessionsHTML = "resources/app/out/vs/sessions/electron-browser/sessions.html"
const devinWorkbenchHTMLHash = "e12c37d6dd14e018fb8528b1b65f328be8d1532d2b8b185fe322df6b582ee29e"
const devinSessionsHTMLHash = "c43e6bd4a547c46ac6f506dd7b95ee703b5c84a0962c6e776fde14403976d9a6"
const devinWorkbenchScript = "resources/app/out/vs/code/electron-browser/workbench/devin-localizer.js"
const devinSessionsScript = "resources/app/out/vs/sessions/electron-browser/devin-localizer.js"
const agyArchiveHash = "8934adef0622c9271c92eea950a71848764a66f7b10023ccbd82e54b959adb0e"
const agyServerHash = "cc94d648c39a67c293d0fdc8befc51e953d1a8f55617bf49fff69885ba475db9"

type fileChange struct {
	Name string
	Data []byte
}
type Pref struct {
	Language string
	Endpoint string
	Model    string
}

func devinPatch(original, workbench, sessions, product []byte, rows []Row, label string) ([]fileChange, error) {
	if hash(original) != devinEntryHash {
		return nil, errors.New("Devin 语言入口与支持的版本不一致")
	}
	if hash(workbench) != devinWorkbenchHTMLHash || hash(sessions) != devinSessionsHTMLHash {
		return nil, errors.New("Devin 会话页面入口与支持的版本不一致")
	}
	const marker = "gu=[{code:"
	start := bytes.Index(original, []byte(marker))
	if start < 0 {
		return nil, errors.New("未找到 Devin 语言列表")
	}
	end := bytes.Index(original[start:], []byte("]})))"))
	if end < 0 {
		return nil, errors.New("Devin 语言列表结构不匹配")
	}
	end += start
	groups := map[string]map[string]string{}
	for _, row := range rows {
		if row.State == "translated" {
			_, key, _ := strings.Cut(row.Key, ".")
			if groups[row.Group] == nil {
				groups[row.Group] = map[string]string{}
			}
			groups[row.Group][key] = row.Text
		}
	}
	generated := append([]byte("export default "), jsonBytes(groups)...)
	generated = append(generated, ';', 10)
	entry := fmt.Sprintf(", {code:'zh-CN',label:%s,translation:()=>import('./local-locale-zh-CN.js').then(m=>m.default)}", jsonBytes(label))
	output := append(append(append([]byte{}, original[:end]...), []byte(entry)...), original[end:]...)
	inject := func(document []byte) ([]byte, error) {
		if bytes.Contains(document, []byte("devin-localizer.js")) {
			return nil, errors.New("原版页面包含未预期的汉化入口")
		}
		lower := bytes.ToLower(document)
		index := bytes.LastIndex(lower, []byte("</body>"))
		if index < 0 {
			return nil, errors.New("Devin HTML 页面结构不匹配")
		}
		result := append([]byte{}, document[:index]...)
		result = append(result, []byte("<script src=\"./devin-localizer.js\"></script>\n")...)
		result = append(result, document[index:]...)
		return result, nil
	}
	workbench, err := inject(workbench)
	if err != nil {
		return nil, err
	}
	sessions, err = inject(sessions)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err = parseJSON(product, &doc); err != nil || doc["checksums"] == nil {
		return nil, errors.New("Devin product.json 校验清单结构不匹配")
	}
	var checksums map[string]string
	if err = parseJSON(doc["checksums"], &checksums); err != nil || checksums == nil {
		return nil, errors.New("Devin product.json 校验清单结构不匹配")
	}
	for name, data := range map[string][]byte{"vs/code/electron-browser/workbench/workbench.html": workbench, "vs/sessions/electron-browser/sessions.html": sessions} {
		if _, ok := checksums[name]; !ok {
			return nil, fmt.Errorf("Devin 校验清单缺少 %s", name)
		}
		sum := sha256.Sum256(data)
		checksums[name] = base64.RawStdEncoding.EncodeToString(sum[:])
	}
	doc["checksums"] = jsonBytes(checksums)
	formatted, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		return nil, err
	}
	product = append(formatted, '\n')
	runtime := append([]byte{}, asset("assets/devin-runtime.js")...)
	runtime = append(runtime, '\n')
	return []fileChange{
		{devinData, generated},
		{devinEntry, output},
		{devinProduct, product},
		{devinWorkbenchHTML, workbench},
		{devinSessionsHTML, sessions},
		{devinWorkbenchScript, runtime},
		{devinSessionsScript, runtime},
	}, nil
}

type archiveNode struct {
	Files      map[string]*archiveNode `json:"files,omitempty"`
	Size       *int                    `json:"size,omitempty"`
	Offset     string                  `json:"offset,omitempty"`
	Unpacked   bool                    `json:"unpacked,omitempty"`
	Executable bool                    `json:"executable,omitempty"`
	Link       string                  `json:"link,omitempty"`
	Integrity  *archiveIntegrity       `json:"integrity,omitempty"`
}
type archiveIntegrity struct {
	Algorithm string   `json:"algorithm"`
	Hash      string   `json:"hash"`
	BlockSize int      `json:"blockSize"`
	Blocks    []string `json:"blocks"`
}

func openArchive(data []byte) (*archiveNode, []byte, error) {
	if len(data) < 16 || binary.LittleEndian.Uint32(data) != 4 {
		return nil, nil, errors.New("无效的 Electron 资源文件")
	}
	offset := int(binary.LittleEndian.Uint32(data[4:])) + 8
	amount := int(binary.LittleEndian.Uint32(data[12:]))
	if offset > len(data) || amount <= 0 || amount > offset-16 {
		return nil, nil, errors.New("无效的 Electron 资源索引")
	}
	root := &archiveNode{}
	if err := json.Unmarshal(data[16:16+amount], root); err != nil {
		return nil, nil, err
	}
	return root, data[offset:], nil
}
func archiveFile(root *archiveNode, body []byte, name string) (*archiveNode, []byte, error) {
	node := root
	for _, part := range strings.Split(name, "/") {
		node = node.Files[part]
		if node == nil {
			return nil, nil, fmt.Errorf("资源缺少 %s", name)
		}
	}
	offset, err := strconv.Atoi(node.Offset)
	if err != nil || node.Size == nil || node.Unpacked || offset < 0 || *node.Size < 0 || offset > len(body) || *node.Size > len(body)-offset {
		return nil, nil, fmt.Errorf("资源 %s 的索引无效", name)
	}
	return node, body[offset : offset+*node.Size], nil
}
func packArchive(root *archiveNode, body, appendix []byte) []byte {
	metadata := jsonBytes(root)
	padded := (len(metadata) + 3) &^ 3
	result := make([]byte, 16+padded, 16+padded+len(body)+len(appendix))
	binary.LittleEndian.PutUint32(result, 4)
	binary.LittleEndian.PutUint32(result[4:], uint32(8+padded))
	binary.LittleEndian.PutUint32(result[8:], uint32(4+padded))
	binary.LittleEndian.PutUint32(result[12:], uint32(len(metadata)))
	copy(result[16:], metadata)
	result = append(result, body...)
	return append(result, appendix...)
}

func agyRenderer(rows []Row, pref Pref) string {
	dict := map[string]string{}
	if pref.Language == "zh-CN" {
		for _, row := range rows {
			if row.State == "translated" {
				dict[strings.TrimSpace(row.Source)] = row.Text
			}
		}
	}
	return string(asset("assets/agy-runtime.js")) + "(" + string(jsonBytes(dict)) + ");"
}

func agyPatch(original []byte, rows []Row, pref Pref) ([]fileChange, error) {
	if hash(original) != agyArchiveHash {
		return nil, errors.New("Antigravity 安装资源指纹不匹配")
	}
	if pref.Language == "en" {
		return []fileChange{{agyArchive, original}}, nil
	}
	root, body, err := openArchive(original)
	if err != nil {
		return nil, err
	}
	entry, utils, err := archiveFile(root, body, "dist/utils.js")
	if err != nil {
		return nil, err
	}
	const anchor = "    hubContents.on('will-navigate', guardExternalNavigation);"
	if bytes.Count(utils, []byte(anchor)) != 1 || entry.Integrity == nil || entry.Integrity.BlockSize <= 0 {
		return nil, errors.New("Antigravity 主界面挂载点不匹配")
	}
	hook := fmt.Sprintf(`
    // Desktop language overlay managed by Localizer.
    const applyLocalizerOverlay = () => {
        try {
            const current = new URL(hubContents.getURL());
            const expected = new URL(url);
            if (current.protocol !== 'https:' || current.hostname !== '127.0.0.1' || current.origin !== expected.origin) return;
            const render = %s;
            void hubContents.executeJavaScriptInIsolatedWorld(782, [{code: render}]).catch(error => console.warn('[Language overlay]', error.message));
        } catch (error) {
            console.warn('[Language overlay]', error.message);
        }
    };
    hubContents.on('dom-ready', applyLocalizerOverlay);
    hubContents.on('did-finish-load', applyLocalizerOverlay);
`, jsonBytes(agyRenderer(rows, pref)))
	modified := bytes.Replace(utils, []byte(anchor), []byte(hook+anchor), 1)
	size := len(modified)
	entry.Size = &size
	entry.Offset = strconv.Itoa(len(body))
	entry.Integrity.Hash = hash(modified)
	entry.Integrity.Blocks = nil
	for i := 0; i < len(modified); i += entry.Integrity.BlockSize {
		entry.Integrity.Blocks = append(entry.Integrity.Blocks, hash(modified[i:min(len(modified), i+entry.Integrity.BlockSize)]))
	}
	return []fileChange{{agyArchive, packArchive(root, body, modified)}}, nil
}
