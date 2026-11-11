package ui

import "github.com/egoist/mygo"

type menuBinding struct {
	item                     *mygo.MenuItem
	enabled                  func() bool
	checked                  func() bool
	lastEnabled, lastChecked bool
}

func (s *controller) installMenu() {
	var bindings []menuBinding
	item := func(label string, kind mygo.MenuItemType, enabled, checked func() bool, action func()) *mygo.MenuItem {
		entry := &mygo.MenuItem{
			Label: label, Type: kind,
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) { s.window.Update(action) },
		}
		if enabled != nil {
			entry.Disabled = !enabled()
		}
		if checked != nil {
			entry.Checked = checked()
		}
		if enabled != nil || checked != nil {
			bindings = append(bindings, menuBinding{
				item: entry, enabled: enabled, checked: checked,
				lastEnabled: !entry.Disabled, lastChecked: entry.Checked,
			})
		}
		return entry
	}
	command := func(label string, enabled func() bool, action func()) *mygo.MenuItem {
		return item(label, mygo.MenuItemNormal, enabled, nil, action)
	}
	check := func(label string, value *bool, update func()) *mygo.MenuItem {
		return item(label, mygo.MenuItemCheckbox, nil, func() bool { return *value }, func() {
			*value = !*value
			if update != nil {
				update()
			}
		})
	}
	group := func(label string, entries ...*mygo.MenuItem) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Submenu: entries}
	}
	choice := func(label string, value *string, values, labels []string, update func()) *mygo.MenuItem {
		entries := make([]*mygo.MenuItem, 0, len(values))
		for i, option := range values {
			text := option
			if labels != nil {
				text = labels[i]
			}
			entries = append(entries, item(text, mygo.MenuItemRadio, nil, func() bool { return *value == option }, func() {
				*value = option
				if update != nil {
					update()
				}
			}))
		}
		return group(label, entries...)
	}
	p := &s.settings
	serialClosed := func() bool { return s.session == nil }
	idleDialog := func() bool { return !s.dialogBusy }
	menu := mygo.NewMenu([]*mygo.MenuItem{
		group("通讯端口",
			command("刷新串口列表", serialClosed, s.refreshPorts),
			command("打开/关闭串口", func() bool { return s.session != nil || p.Port.Name != "" }, s.togglePort),
			mygo.Separator(), command("退出", nil, s.window.Close)),
		group("串口设置",
			check("更多串口设置", &s.advanced, nil)),
		group("显示",
			check("HEX 显示", &p.ReceiveHex, s.configureLog),
			check("显示时间戳", &p.Timestamp, s.configureLog),
			check("按空闲分包", &p.Packet, s.configureLog),
			check("显示发送记录", &p.ShowSent, s.configureLog),
			choice("文本编码", &p.Encoding, encodings, nil, s.configureLog),
			mygo.Separator(),
			check("最前", &s.alwaysOnTop, s.applyAlwaysOnTop),
			command("清除窗口", nil, s.log.clear),
			command("保存数据", idleDialog, s.saveLog),
			command("接收数据到文件 / 停止", func() bool { return s.session != nil && !s.dialogBusy }, s.toggleCapture)),
		group("发送",
			command("发送当前内容", s.canSend, func() { s.sendText(*s.sendValue()) }),
			check("HEX 发送", &p.SendHex, nil),
			check("加回车换行", &p.CRLF, nil),
			check("定时发送", &s.repeat, nil),
			choice("附加校验", &p.Checksum, checksums, nil, nil),
			mygo.Separator(),
			command("打开文件", idleDialog, s.chooseFile),
			command("发送文件", s.canSendFile, s.sendFile),
			choice("发送文件延时设置", &p.FileDelay, []string{"0", "1", "10", "50", "100"},
				[]string{"连续发送", "每 256 字节延时 1 ms", "每 256 字节延时 10 ms", "每 256 字节延时 50 ms", "每 256 字节延时 100 ms"}, nil),
			command("停止发送", func() bool { return s.cancelSend != nil }, s.stopSending)),
		group("多字符串",
			check("展开多条发送", &p.ShowPresets, nil),
			command("发送勾选项", s.canSend, func() { s.sendPresets(-1) }),
			check("循环发送勾选项", &s.loopPresets, nil)),
		group("小工具",
			command("收发计数清零", nil, s.clearCounts),
			command("保存参数", nil, s.saveSettings)),
	})
	s.menuBindings = bindings
	s.window.SetMenu(menu)
}

func (s *controller) syncMenu() {
	for i := range s.menuBindings {
		binding := &s.menuBindings[i]
		if binding.enabled != nil {
			enabled := binding.enabled()
			if enabled != binding.lastEnabled {
				binding.item.SetEnabled(enabled)
				binding.lastEnabled = enabled
			}
		}
		if binding.checked != nil {
			checked := binding.checked()
			if checked != binding.lastChecked {
				if binding.item.IsChecked() != checked {
					binding.item.SetChecked(checked)
				}
				binding.lastChecked = checked
			}
		}
	}
}
