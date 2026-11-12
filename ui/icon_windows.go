package ui

import (
	"fmt"

	_ "github.com/fcying/SerialGo/assets" // Link the executable's icon resources.
	"github.com/fcying/SerialGo/internal/i18n"

	"golang.org/x/sys/windows"
)

var (
	iconUser32           = windows.NewLazySystemDLL("user32.dll")
	iconLoadImage        = iconUser32.NewProc("LoadImageW")
	iconSendMessage      = iconUser32.NewProc("SendMessageW")
	iconDestroy          = iconUser32.NewProc("DestroyIcon")
	iconGetDPI           = iconUser32.NewProc("GetDpiForWindow")
	iconGetMetricsForDPI = iconUser32.NewProc("GetSystemMetricsForDpi")
)

// Use the matching ICO image instead of resampling the 256px window PNG.
func (s *controller) setWindowIcon() error {
	var module windows.Handle
	err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, nil, &module)
	if err != nil {
		return err
	}
	hwnd := s.window.NativeHandle()
	var handles [2]uintptr
	var currentDPI uintptr
	hasDPI := iconGetDPI.Find() == nil
	hasMetrics := iconGetMetricsForDPI.Find() == nil
	refresh := func() error {
		dpi := uintptr(96)
		if hasDPI {
			value, _, callErr := iconGetDPI.Call(hwnd)
			if value == 0 {
				return i18n.WrapError("获取窗口 DPI 失败", "Failed to get window DPI", callErr)
			}
			dpi = value
		}
		if currentDPI == dpi {
			return nil
		}
		metrics := [...]uintptr{49, 11} // SM_CXSMICON, SM_CXICON.
		defaults := [...]uintptr{16, 32}
		var next [2]uintptr
		for i, metric := range metrics {
			size := (defaults[i]*dpi + 48) / 96
			if hasMetrics {
				value, _, _ := iconGetMetricsForDPI.Call(metric, dpi)
				if value > 0 {
					size = value
				}
			}
			// appicon emits icon group 1; LoadImage selects its native-size entry.
			icon, _, callErr := iconLoadImage.Call(uintptr(module), 1, 1, size, size, 0)
			if icon == 0 {
				for _, handle := range next {
					if handle != 0 {
						iconDestroy.Call(handle)
					}
				}
				return i18n.WrapError(fmt.Sprintf("加载 %dpx 窗口图标失败", size), fmt.Sprintf("Failed to load %dpx window icon", size), callErr)
			}
			next[i] = icon
		}
		for i, handle := range next {
			iconSendMessage.Call(hwnd, 0x80, uintptr(i), handle) // WM_SETICON.
			if handles[i] != 0 {
				iconDestroy.Call(handles[i])
			}
		}
		handles, currentDPI = next, dpi
		return nil
	}
	err = refresh()
	if err != nil {
		return err
	}
	update := func() {
		refreshErr := refresh()
		if refreshErr != nil {
			s.window.Update(func() { s.app.Fail("设置窗口图标失败", "Failed to set window icon", refreshErr) })
		}
	}
	s.window.OnMove(update)
	s.window.OnResize(update)
	s.window.OnClosed(func() {
		for _, handle := range handles {
			iconDestroy.Call(handle)
		}
	})
	return nil
}
