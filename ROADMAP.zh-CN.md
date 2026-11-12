# SerialGo 路线图

[English](ROADMAP.md) | 简体中文

[使用说明](README.zh-CN.md) | [架构说明](ARCHITECTURE.zh-CN.md)

本文记录延后工作及其验收条件, 不代表已实现功能或发布时间表. 开始试用前须重新核查已有依赖评估.

## 串口库迁移评估与延后计划

**结论: 值得隔离试用, 但必须等其他应用功能回归测试完成后再进行. 当前没有替换依赖或串口后端.** 目前仍使用 `github.com/albenik/go-serial/v2 v2.6.1`, 应用尚未提供自动流控选项.

### 候选与维护情况

- 当前库最新标签仍为 [`v2.6.1`, 对应源码提交日期为 2024-01-28](https://proxy.golang.org/github.com/albenik/go-serial/v2/@latest), [默认分支最后提交](https://github.com/albenik/go-serial/commit/594ef15cc4760409acc4074b486375a6de28bf54)也在同一天. 仓库未归档, 但维护停滞确实是风险, 不能据此认为近期仍在活跃维护.
- 原始上游 [`go.bug.st/serial v1.8.0`](https://github.com/bugst/go-serial/releases/tag/v1.8.0) 发布于 2026-07-15, 维护比当前库更新, 但稳定版接口仍不含流控; [RTS/CTS PR #201](https://github.com/bugst/go-serial/pull/201) 尚未合并.
- 试用候选为 [`github.com/briardhq/go-serial v1.8.0-briard.1`](https://github.com/briardhq/go-serial/tree/v1.8.0-briard.1), 对应提交 `7fa2b2eddb3a0968a2eea9467c39e33e098f526b`, 提交日期为 2026-09-30. 流控实现在 `briard` 分支, 不在 `master`. 它基于上游 v1.8.0, 加入 RTS/CTS 补丁及 Windows RTS 握手修正. 其 [README](https://github.com/briardhq/go-serial/blob/v1.8.0-briard.1/README.md) 明确说明只是等待上游发布该功能前的临时 fork, 不能当作长期维护承诺. 如果正式迁移前上游已提供等效稳定支持, 优先选择上游.

### 已确认的实现与迁移阻碍

- 候选库声明 Go 1.25.0, 模块依赖只有 `golang.org/x/sys`. 当前构建工具链 Go 1.27.4 已满足该工具链下限, 但这不能替代下述 I/O 行为验证.
- 源码中 `Mode.RTSCTSFlowControl` 会设置 Linux `CRTSCTS`, 并在 Windows 同时启用 CTS 发送门控及驱动自动管理 RTS 握手. 它**不提供** XON/XOFF 软件流控或 DTR/DSR 流控. 这些结论来自源码检查, 不是硬件验证.
- `SetMode` 没有应用流控字段的变更. 除非另行修正并验证, 否则切换流控应通过关闭并重新打开串口完成.
- 当前打开串口后会手动调用 `SetRTS`. 候选库的 Windows `SetRTS` 会替换 RTS 握手位, 因而迁移时必须在硬件流控接管 RTS 后避开该调用, 包括仅修改 DTR 的场景. 手动 DTR 及未启用流控时的手动 RTS 功能仍须保留.
- 候选库使用 `Port` 接口及 `Mode` 配置, 不是当前的具体端口类型和 option 函数. 它有 `SetReadTimeout`, 但没有公开的写超时接口. 必须保留应用的 10ms 接收空闲轮询和 250ms 写入期限, 不能悄悄把有界写入改成无限等待.
- 其 Unix 实现打开后仍把描述符切为阻塞模式. 现有非阻塞 I/O 和借用描述符的兼容处理既不能直接删除, 也不能原样照搬. Windows 现有端口枚举及设备描述应保留, 不能因换库引入主动 USB 探测.

### 历史接收兼容性门槛

用户提供的 UART 换库历史是新增的迁移阻碍, 不是现在换回去已经安全的证据. 提交 `8873737` (2024-08-29) 将实际 UART 入口切到 albenik, 并明确记录首字节超时策略会 "break rx data", 而 `SetReadTimeout` 配合 `ReadyToRead` 更好、更快. 后来的互斥锁修改与最初换库原因是两件事. 当时的 `go.bug.st/serial v1.6.2` 实际通过 replace 使用 [`github.com/fcying/go-serial` 的 `312149864b96`](https://github.com/fcying/go-serial/tree/312149864b9635cb5702daf7966eeca833c8755a), 对照时不能假定它就是未修改的上游版本.

对照旧 fork 和 `v1.8.0-briard.1` 的源码, **不能认定历史接收问题已修复**:

- 两者在 Windows 的正数读超时下仍使用首字节返回配置, 即 `ReadIntervalTimeout` 和 `ReadTotalTimeoutMultiplier` 均为 `MAXDWORD`. 新版 CH340 处理把无限或极大超时常量改为 `0x7FFFFFFE`, 不能据此证明当年正数 1ms 超时路径的问题已修复.
- 旧 fork 在读超时且无数据时返回 `Timeout` 错误, 候选库在配置了读超时时返回 `(0, nil)`. 适配层必须保持调用者预期, 不能把这项语义变化当成已修复丢数据的证据.
- UART 当前包装层采用 `ReadyToRead` 轮询、立即读取和外部期限控制. SerialGo 当前 Windows 后端则使用 10ms 首字节超时, `Session.receive` 持续复制每次返回的全部字节. 两者消费方式不同, 一次 `Read` 完成不等于一个协议帧; 显示分包和原始字节是否完整也要分开判断.
- Albenik 也提供 `SetFirstByteReadTimeout`, 但 UART 实现刻意避开了它. 迁移应保留这套轮询规避方式, 或证明等效接收行为可靠, 不能仅凭库版本更新就接受迁移.
- 已审查的 briard `Port` 接口没有公开的 `ReadyToRead` 方法, 因而不能直接照搬 UART 的规避方案, 也不能假定其 `SetReadTimeout(0)` 等同于 albenik 的立即读取模式. 等效等待和读取路径需要单独评估. 当前 SerialGo 策略与 UART 不同, 但仅凭历史记录也不能认定 SerialGo 存在丢数据故障.
- UART 当前 `ReadLength` 还会先读入较大缓冲区, 再把复制长度截到目标剩余长度, 没有保留超出的已读字节. 多帧同时到达时, 必须将这类上层截断与库缺陷分开排查. 本次评估不修改 UART 代码.

正式考虑换库前, 优先在同型号适配器和驱动上复现当年的接收负载. 以相同数据、波特率、超时和接收负载, 对比已经可用的 UART 轮询策略与固定版本候选: 单字节到达、包头后延迟到达包体、连续多帧、突发数据、持续流量、超时后再来数据. 用原始字节顺序、序号或校验及总字节数检查丢失、重复和截断, 不能只对比单次 `Read` 长度或显示换行. 测试调用方的定长读取超额消费问题也应先排除, 避免污染比较结论. 缺少这些证据时, 历史问题仍属未验证; 即使 RTS/CTS 生效也不能据此接受迁移.

### 延后试用与采用条件

1. 先完善基础应用功能并完成回归测试, 再重新核查上游和 fork 的维护情况、可用版本、流控修复、接收接口及历史超时问题, 然后决定是否试用迁移. 本节只是当前评估记录, 不是届时自动换库的批准. 如果复查后仍值得试用, 再作为独立后端变更实施, 固定届时重新审查的标签和提交, 不跟随浮动分支.
2. 保持 `communication/serialport` 包装层契约、EXE 同目录配置、已保存的帧格式和控制线值、预设及 UI 行为不变. 在后端内适配打开、超时、错误和句柄所有权; 不能只换 import 就算完成迁移.
3. 采用前验证 Windows 实际串口收发、自定义波特率、文件发送、分包间隔显示、手动 RTS/DTR、CTS 阻塞与恢复、驱动管理 RTS、写超时、停止和关闭响应、断线重连及反复开关串口. 使用真正支持握手信号的硬件或虚拟环境, 仅启动 UI 不算通过.
4. Linux 继续按现有约定默认只构建. 未实际验证时, 不宣称 Linux I/O 或硬件流控通过. 只有现有超时、取消保证及 Windows 串口行为得到实际证明后, 才接受此次试用迁移.

