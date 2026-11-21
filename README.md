# Localizer · 界面汉化工具

面向 Windows 桌面的 **Devin** 与 **Antigravity** 界面词典管理与原地汉化工具，基于 Go + Wails 3 构建。

## 功能特性

- **自动发现本机安装与安全回滚**：自动从系统注册表定位 `Devin` 与 `Antigravity` 安装目录；首次写入前自动校验并持久化官方原版资源至 `localizer-data/<id>/originals/`，配合原子事务账本支持随时一键字节级恢复英文原版。
- **高覆盖内置词库**：内置校订词典（`Devin` 3.14.30833 与 `Antigravity` 2.34.0 全量设置、权限档位及智能体工作台界面文案），严格保护代码块、终端、用户输入与 Markdown 回复正文。
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
- `Devin` 基础译文部分参考社区开源词库，原始版权声明保留于 `assets/devin-license.txt`。
