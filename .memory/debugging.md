# Debugging Lessons

## ANSI 滚动区域 + \r\n 导致空行 (2026-03-10)

**问题**: 欢迎页的 icon 行之间出现空行，改 icon 字符、改数据结构都无效。

**根因**: `writeOutputLine` 和 `writeOutput` 都使用 `\r\n` 在滚动区域底部触发逐行滚动：
```go
// 两个方法本质一样，都是逐行 \r\n
fmt.Fprintf(r.writer, "\033[s\033[%d;1H\r\n%s\033[u", scrollBottom, line)
```
当行内容包含 ANSI 颜色转义序列（如 lipgloss 的 `\033[38;2;r;g;bm`）时，某些终端在处理 `\r\n` + ANSI 序列时会多产生一个空行。这不是字符宽度问题（换成 ASCII box drawing 字符也一样有空行）。

**修复**: 欢迎页改用 **光标绝对定位**，完全不用 `\r\n` 滚动：
```go
// 直接定位到第 i 行，清行后写内容
fmt.Fprintf(r.writer, "\033[%d;1H\033[2K%s", row, line)
```

**教训**:
1. `writeOutput` 内部也是逐行 `\r\n`，把多行 Join 后传给它并不能解决问题
2. ANSI 滚动区域的 `\r\n` 行为在不同终端实现中有差异，带颜色的行更容易触发
3. 对于初始渲染（如欢迎页），直接光标定位比滚动机制更可靠
4. 滚动机制 (`\r\n`) 适合交互过程中动态追加内容，不适合一次性渲染多行

## 交叉编译与部署注意事项

- macOS 编译的二进制不能在 Linux 运行：必须 `GOOS=linux GOARCH=amd64`
- "文本文件忙" 错误：旧进程还在运行，先 `pkill opendb; sleep 1`
- 部署前必须 `rm -f` 旧文件再 `scp`，否则 scp 可能失败
- **发版必须更新 PATH 路径**：用户通过 `opendb` 启动（实际路径 `/usr/local/bin/opendb`），scp 到 `/home/oracle/` 后必须 `cp /home/oracle/opendb /usr/local/bin/opendb`，否则用户运行的还是旧版本

## dbtop 数值全为 0 — go-ora Number 类型问题 (2026-03-11, 未解决)

**现象**: dbtop 除了 QPS 以外，SGA/PGA/SN/AN/ASC/ASI/IDL/db%/WTR%/等待事件/活跃会话全部为 0 或空。

**已确认的根因**:
1. **go-ora 返回 `go_ora.Number` 自定义类型** — Oracle NUMBER 列不是 Go 原生类型
2. `go_ora.Number` 的 `String()` 签名是 `(string, error)` 而非 `String() string`，不满足 `fmt.Stringer` 接口
3. `convertOracleValue` 的 default 分支用 `fmt.Sprintf("%v", v)` 转换，得到的是结构体内部字节 `{[128 ...]}` 而非数字
4. `Float64()` / `Int64()` / `String()` 都是 **指针接收者** `*Number`

**已做的修复（部分生效）**:
- `toInt()` 加了 string 类型处理 → QPS 开始工作（sysstat 查询可能返回的类型不同）
- `sessionCountSQL` 的 `ASC` 别名是 Oracle 保留字 → 改为 `ACPU`
- `convertOracleValue` 加了 `go_ora.Number` 和 `*go_ora.Number` 的 case → 部署后仍然为 0

**待排查**:
- 为什么 QPS (sysstat) 能工作但其他不行？可能 sysstat 返回的不是 `go_ora.Number`
- `go_ora.Number` 的方法是指针接收者，type switch 中值类型 `v` 调用 `v.Float64()` 是否有问题
- 需要加临时日志打印 `reflect.TypeOf(val)` 确认实际类型
- 可能 go-ora 对不同查询返回不同类型（有些走 Scan 有些走其他路径）
- 检查 driver.go 中 `scanRows` 的实际扫描逻辑

**下一步**:
1. 在 `convertOracleValue` 的 default 分支加 `log.Printf("unknown type: %T = %v", val, val)` 临时日志
2. 在服务器上跑一次 dbtop 看日志输出，确认实际收到的类型
3. 根据实际类型修复转换逻辑
4. 也检查 `scanRows` 是否用了 `sql.ColumnType.ScanType()` 来决定扫描类型

**相关文件**:
- `internal/db/oracle/typeconv.go` — 类型转换
- `internal/db/oracle/driver.go` — 扫描行逻辑
- `internal/monitor/dbtop/collector.go` — dbtop 数据采集
- go-ora 源码: `~/go/pkg/mod/github.com/sijms/go-ora/v2@v2.9.0/number.go`

## Unicode 歧义宽度字符

- `▗▄▖▝▀▘` 等 block 字符 `east_asian_width=A`（歧义）
- `go-runewidth` 在 CJK 环境默认算宽度 2，但多数终端渲染为宽度 1
- 需要用 `iconVisW` 手动覆盖宽度值
