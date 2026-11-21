package main

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

func (s *Service) Import(id string) (bool, error) {
	dialog := application.Get().Dialog.OpenFile().SetTitle("导入 JSON 词典").CanChooseFiles(true).CanChooseDirectories(false).AddFilter("JSON", "*.json")
	if s.window != nil {
		dialog.AttachToWindow(s.window)
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil || path == "" {
		return false, err
	}
	return true, s.manualImport(id, path)
}
func (s *Service) Export(id string) (bool, error) {
	if _, err := getTarget(id); err != nil {
		return false, err
	}
	dialog := application.Get().Dialog.SaveFile().SetMessage("导出 JSON 词典").SetFilename(id+"-zh-CN.json").AddFilter("JSON", "*.json")
	if s.window != nil {
		dialog.AttachToWindow(s.window)
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil || path == "" {
		return false, err
	}
	return true, s.manualExport(id, path)
}

func main() {
	if err := run(); err != nil {
		showFatal(err.Error())
	}
}
func run() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(exe), "localizer-data")
	if err = os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("请把 EXE 放在可写目录：%w", err)
	}
	service := makeService(root)
	content, _ := fs.Sub(resources, "ui")
	qaServer := os.Getenv("LOCALIZER_QA_SERVER") == "1"
	var singleInstance *application.SingleInstanceOptions
	if !qaServer {
		singleInstance = &application.SingleInstanceOptions{UniqueID: "desktop-localizer-9e51e6"}
	}
	var qaHandler http.Handler
	app := application.New(application.Options{
		Name:        "界面汉化工具",
		Description: "管理 Devin 和 Antigravity 的界面词典",
		Icon:        asset("assets/appicon.png"),
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(content),
			Middleware: func(next http.Handler) http.Handler {
				qaHandler = next
				return next
			},
		},
		Services:   []application.Service{application.NewService(service)},
		OnShutdown: func() { service.Cancel() },
		Windows: application.WindowsOptions{
			WebviewUserDataPath:           filepath.Join(root, "webview"),
			DisableQuitOnLastWindowClosed: qaServer,
		},
		SingleInstance: singleInstance,
	})
	if qaServer {
		port := os.Getenv("WAILS_SERVER_PORT")
		if port == "" {
			port = "43125"
		}
		srv := &http.Server{
			Addr: "127.0.0.1:" + port,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if qaHandler != nil {
					qaHandler.ServeHTTP(w, r)
				}
			}),
		}
		go func() { _ = srv.ListenAndServe() }()
		return app.Run()
	}
	hiddenWindow := os.Getenv("LOCALIZER_QA_HIDDEN") == "1"
	window := application.WebviewWindowOptions{
		Title:            "界面汉化工具",
		Width:            1081,
		Height:           773,
		MinWidth:         761,
		MinHeight:        551,
		Frameless:        true,
		Hidden:           hiddenWindow,
		BackgroundColour: application.NewRGBA(238, 241, 246, 255),
		URL:              "/",
		Windows: application.WindowsWindow{
			NonClientRegionSupport:     true,
			WebView2CompositionHosting: !hiddenWindow,
		},
	}
	service.window = app.Window.NewWithOptions(window)
	return app.Run()
}
