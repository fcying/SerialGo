# AGENTS.md

## 架构
- `main.go` 是程序入口. `appName` 和 `appVersion` 定义应用名称和版本, 窗口标题仅使用 `appName + " v" + appVersion`.
- `ui/` 使用 MyGo `ui.View` 构建原生界面, 不依赖 WebView、Node.js 或 TypeScript 绑定. `ui/controller.go` 管理界面状态、后台任务和取消; `ui/view.go` 定义布局和控件. 后台 I/O 不阻塞 UI 主线程, 结果通过 `Window.Update` 更新.
- `communication/serialport/` 封装设备枚举与 I/O; `internal/` 管理串口会话和数据编解码; `config/` 负责参数持久化. 依赖版本固定在 `go.mod`.
- 图标只维护 `assets/icon.svg`. `cmd/appicon/` 使用现有 SVG 渲染器生成 PNG、多尺寸 ICO 和 Windows `.syso` 资源, 均不提交或手工修改. PNG 在构建时嵌入程序供窗口使用.

## 编译与测试
- 应用编译统一用 `just`, 不新增独立编译脚本. `justfile` 采用 Bash 默认脚本模式. 构建要求 Go 1.27.3 或更新版本.
- 构建脚本的日志使用英文.
- `just` 默认执行 `build`, 由 `build` 调用 `just windows`. `windows` 是独立编译入口, 生成 `build/windows-amd64/SerialGo.exe`; `just linux` 生成 `build/linux-amd64/SerialGo`. 不使用 alias, 默认构建不自动运行程序.
- Windows 和 Linux 构建都会先执行 `_icon` 生成图标资源. 从干净工作区直接运行聚焦 Go 测试前, 先执行 `just _icon`.
- `just test` 直接依赖 `windows`, 不依赖可能改变目标的 `build`. 若 `/mnt/d/project/SerialGo` 已存在, 只更新其中的 `SerialGo.exe`; 否则跳过复制, 不创建目录. 复制失败时使用 `wslpath` 转换目标路径, 通过 `/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe` 结束该路径的 Windows 进程, 再重试复制一次. 最终复制成功后进入发布目录, 用 `setsid` 后台启动 EXE. 不重复编译, 不改配置.
- 根据修改范围选择聚焦测试. 编码和校验用例位于 `internal/`, 接收显示用例位于 `ui/`. Linux 默认只构建, 不运行测试.

## Windows 测试机
- `cth-win10` 是测试机. 用 `ssh -o BatchMode=yes cth-win10 hostname` 检查直连, 不经宿主机跳转. 不在仓库中保存连接凭据.
- 用 `just windows` 生成 Windows 产物, 用 `scp` 更新测试机桌面的 `SerialGo/SerialGo.exe`. 固定使用这一个目录, 不按版本另建目录. 更新时只替换 EXE, 保留已有 `config.toml`. 聚焦 Go 测试可用 `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o <测试程序.exe> <包>` 交叉编译, 上传后在测试机执行.
- GUI 验证在已登录的桌面会话中启动. 如使用交互式计划任务, 使用本次测试专用名称. 不覆盖用户已有的配置.
- 没有物理或虚拟 COM 口时, 只报告实际验证的界面、参数及错误路径, 不声称真实串口收发通过. 不为测试安装驱动或修改共用环境.
- 验证结束后, 保留 `桌面/SerialGo/SerialGo.exe` 和已有配置. 只清理本次创建的进程、计划任务、测试脚本和其他临时文件.
