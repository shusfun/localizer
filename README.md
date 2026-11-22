# Localizer · 界面汉化工具

面向 Windows 桌面的 **Devin** 与 **Antigravity** 界面词典管理与汉化工具，基于 Go + Wails 3 构建。

## 功能特性

- **Devin 中文启动**。退出 Devin 后点「准备中文启动」，再点「启动应用」。首次安装语言包后，如果首次启动仍显示英文，请完整退出 Devin，再从 Localizer 启动一次。编辑器使用内置语言包，会话页和 Devin 专属设置使用内置词典。已有会话语言偏好可在 Devin「首选项 → 显示语言」改为简体中文。Localizer 词典可继续查看、编辑和导出。
- **恢复 Devin**。旧补丁导致「安装损坏」时，先退出 Devin，再尝试「恢复原版」。如果资源已被其他工具改写，停用会在启动时写入安装目录的旧汉化扩展，并使用同版本官方安装包修复。
- **Devin 启动参数报错**。如果 Devin 提示 argv.json 包含错误，请打开提示指向的运行参数文件，修正语法后重启。Localizer 通过启动参数指定语言。
- **Antigravity 汉化**。自动定位安装目录，验证原版资源，在首次写入时保存备份，支持恢复英文。两款应用的校订词典均随构建内置。
- **词条精修与占位符强校验**：支持按状态与分组检索、行内编辑、一键插入 `{{variable}}` / `<code>` 等受保护片段、专有词保留原文，以及 JSON 词典导入导出。
- **Mistral 本地模型智能补译**：自动识别运行中的 `mistral-gui` 桌面守护服务（支持 DPAPI 鉴权与空闲引擎自动唤醒）或本机 OpenAI 兼容推理接口，按需批量补译新增缺口词条。

## 构建与测试

```powershell
# 运行单元测试
go test ./...

# 编译正式版单文件 EXE（需 Windows x64 环境）
go build -tags production -ldflags="-s -w -H windowsgui" -o Localizer.exe .
```

## 致谢与许可

- 项目代码采用 MIT 许可证发布。
- `Devin` 基础译文部分参考社区开源词库，内置语言包只含语言资源和扩展声明。原始版权声明保留于 `assets/devin-license.txt`。
