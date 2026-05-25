# DBAA 分支待办

dbaa（中国农业银行客制化版本）已发现的 bug / 改进项跟踪。dbaa 与 opendb
共享代码 99%，所以这里的修复同时受益 opendb（除非明确仅 dbaa 范围）。

---

## 0. v1.2.24 后续优化：Qwen3-32B current-db 输出继续向 Opus 对齐（v1.2.25 已完成门禁）

**背景**: v1.2.24 后，Qwen3-32B PromptToolAdapter 已能稳定走 current-db 受控证据路径，输出质量接近 Opus，但在证据边界、占比数字、后台线程因果、风险/回滚表达上仍弱于 Opus。

**状态**: v1.2.25 已实现 current-db prompt 约束、输出门禁和 golden/单测，后续按现场新样本继续补充。

**已落地 / 持续回归项**:

1. **历史累计视图边界约束**
   - dbe_perf.statement / topsql / slowsql 必须明确标注为历史累计统计。
   - 只有 activesessions / waits / blocktree 等当前快照能支撑“当前在线故障”。
   - 若历史 Top SQL 无当前快照呼应，只能表述为“历史统计污染 / 历史负载痕迹”，不能判定当前故障。

2. **百分比/占比必须来自工具计算**
   - 例如“fault_* + pg_sleep 占比 99.97%”必须由 evidence builder 计算后提供。
   - LLM 不得自行估算或编写精确百分比。
   - 未计算时用“占比较高 / 占据 Top SQL 前列”等非精确表达。

3. **后台线程因果边界**
   - WLM fetch collect info 等后台线程只能列为“观察项”。
   - 不允许把后台线程常驻时间和 fault_* / pg_sleep 历史耗时直接建立因果链。
   - 输出中应区分“后台常驻线程”“业务会话”“压测/故障注入历史 SQL”。

4. **建议动作统一补风险 / 前置检查 / 回滚**
   - current-db 诊断里的所有可执行动作，尤其 reset_unique_sql、TRUNCATE、kill、参数变更，都应包含风险、前置检查、回滚/不可逆说明。
   - 只读排查 SQL 标注“无副作用”。
   - 不可逆动作必须提示先导出/备份。

5. **加入 golden 回归**
   - 将 Qwen3-32B prompt 与 Opus 的 current-db 对比固化成 golden 规则。
   - 禁止未来源的精确占比。
   - 禁止后台线程与历史 fault SQL 建立伪因果。
   - 要求历史/当前边界、风险、前置检查、回滚字段齐全。

**优先级**: P1（输出质量和小模型安全边界；不影响 v1.2.24 当前可用性）

---


## 0.1 /trace 命令语义拆分：OS trace 与诊断链路 trace 避免混淆（v1.2.25 已完成）

**背景**: 裸 `/trace` 的原始设计是 OS / openGauss 进程堆栈 trace，用于抓数据库进程信息；v1.2.24 新增的诊断链路 trace 目前复用 `/trace last` / `/trace history` 子命令，容易让用户误以为 `/trace` 默认就是诊断链路 trace。

**状态**: v1.2.25 已新增 `/diagtrace`，`/trace last/history` 保留兼容并提示迁移。

**已落地 / 持续回归项**:

1. 保留裸 `/trace` = OS / openGauss 进程 trace，不破坏原设计。
2. 新增 `/diagtrace` 命令，专门用于诊断链路 trace：
   - `/diagtrace last`
   - `/diagtrace last --json`
   - `/diagtrace history [N]`
   - `/diagtrace history --json [N]`
3. 保留 `/trace last` / `/trace history` 兼容路径，但输出提示：这是诊断链路 trace，推荐使用 `/diagtrace last`。
4. 优化裸 `/trace` 报错文案：
   - 当前含义: OS 进程 trace。
   - 未找到 openGauss 进程时，提示“如需查看最近一次 AI/诊断链路，请用 `/diagtrace last`”。
5. 补 golden / 单测：
   - `/trace` 不应误走 diagtrace。
   - `/diagtrace last` 应读取 diagtrace last。
   - `/trace last` 仍兼容并带迁移提示。

**优先级**: P1（UX/命令语义清晰度；不影响诊断主链路）

---

## 1. 连接测试结果显示 "openGauss 5.0.0"，dbaa 应显示 "GaussDB 5.0.0"

**位置**: `internal/setup/conntest.go:253`
```go
b.WriteString(SuccessLine(fmt.Sprintf("Connection successful (%s)", s.dbVersion)) + "\n\n")
```

**问题**: `s.dbVersion` 由数据库 driver 直接返回，对 OG 来说是字面量
"openGauss 5.0.0"。dbaa build 应改成 "GaussDB 5.0.0"。

**修复方案**: 在 conntest.go 渲染时做 brand 替换，`s.dbVersion` 含
"openGauss" 时替换为 `brand.Current().OpenGaussDisplay`：
```go
displayVer := strings.Replace(s.dbVersion, "openGauss", brand.Current().OpenGaussDisplay, 1)
b.WriteString(SuccessLine(fmt.Sprintf("Connection successful (%s)", displayVer)) + "\n\n")
```

**优先级**: P1（dbaa 用户可见错位）

---

## 2. "Result: 0/0 必要权限就绪" — opengauss 没配权限检查清单

**位置**: `internal/setup/conntest.go:265` + `permission.go::PermissionGuideFor`

**根因**: `PermissionCheckQueries(dbType string)` 在 conntest.go:19 定义的
case 只有 oracle / mysql / postgres，**没有 opengauss case** → 走 default
返回 nil → `requiredCount = 0`。

同样 `PermissionGuideFor()`（permission.go）也没有 opengauss case → 显示
权限指南为空。

**修复方案**: 在两个函数都加 opengauss case：

`PermissionCheckQueries("opengauss")` 应包含：
```go
case "opengauss":
    return []PermCheckQuery{
        {"Connect", "SELECT 1"},
        {"pg_stat_activity", "SELECT COUNT(*) FROM pg_stat_activity"},
        {"pg_stat_database", "SELECT COUNT(*) FROM pg_stat_database"},
        {"pg_locks", "SELECT COUNT(*) FROM pg_locks"},
        {"dbe_perf access", "SELECT COUNT(*) FROM dbe_perf.statement LIMIT 1"},
        {"gs_stat views", "SELECT COUNT(*) FROM gs_stat_activity LIMIT 1"},
    }
```

`PermissionGuideFor("opengauss")` 应包含：
```go
case "opengauss":
    return PermissionGuide{
        DBType: "opengauss",
        Required: []PermissionItem{
            {"CONNECT", "连接数据库"},
            {"pg_stat_activity SELECT", "会话状态查询"},
            {"dbe_perf SELECT", "OG 扩展统计视图"},
            {"gs_* SELECT", "OG 特有视图（gs_stat_activity 等）"},
            {"pg_read_all_settings", "查看所有配置参数"},
        },
        NotRecommended: []PermissionItem{
            {"SUPERUSER / SYSADMIN", "权限过大"},
            {"AUDITADMIN", "包含审计权限，非诊断必需"},
        },
        CreateSQL: "CREATE USER opendb WITH PASSWORD '<password>';\n" +
            "GRANT CONNECT ON DATABASE <dbname> TO opendb;\n" +
            "ALTER USER opendb WITH MONITOR;  -- OG 监控角色\n" +
            "GRANT SELECT ON ALL TABLES IN SCHEMA dbe_perf TO opendb;",
    }
```

**优先级**: P0（功能缺失，影响 OG/GaussDB 连接体验）

**影响**: opendb + dbaa 都受益（OG 连接配置都需要这个）

---

## 3. "最近使用连接" 标题误导，列的是所有已配置连接

**位置**: `internal/ui/repl_welcome.go:104-144`

**根因**: 代码逻辑实际是"已配置连接 + 按最近使用排序"，不是"只显示用过的"。
`recentNames` 来自 `connMgr.RecentConnections()`（用过的），但 line 118-122
之后 `for _, c := range allConns` 把所有未在 recent 列表的连接也追加进去。

```go
// 已 used 的排前面
for _, name := range recentNames { ... }
// 剩下的所有 conn 也加进去
for _, c := range allConns {
    if !seen[c.Name] {
        orderedConns = append(orderedConns, c)
    }
}
```

所以即使用户从未 /login 过 oracle，oracle 仍出现在"最近使用连接"里，因为它
存在 `~/.dbaa/config.yaml` 的 `connections:` 清单。

**用户实际看到的**: dbaa 启动时显示了 oracle / mysql，因为之前
`cp -r ~/.opendb ~/.dbaa` 把 opendb 的 connections 清单全拷过来了。

**修复方案 A**: 标题改成 "已配置连接"（更准确，不改逻辑）。
**修复方案 B**: 真正只显示 recent 用过的，无 recent 时显示 "暂无最近连接"
（改逻辑但 UX 退化 — 新用户看不到可点的连接）。

**推荐**: 方案 A（改标题），改动最小，用户预期匹配实际行为。

**优先级**: P2（不影响功能，仅文案）

---

## 4. PROFILE.md 模板硬编码 "openGauss" — dbaa 应显示 "GaussDB"

**位置**: `internal/engine/memory/profile.go::openGaussProfileTemplate`

**当前问题**: 模板字符串硬写：
```go
return fmt.Sprintf(`# 实例画像: %s (openGauss)
...
- **openGauss 版本**: 待探测（SELECT version()）
...`, instance, ...)
```

dbaa build 也走这个模板，所以 `~/.dbaa/memory/gaussdb/PROFILE.md` 里写
"openGauss"，与 dbaa 的 GaussDB 品牌不一致。

**修复方案**:
```go
displayName := brand.Current().OpenGaussDisplay  // "openGauss" or "GaussDB"
return fmt.Sprintf(`# 实例画像: %s (%s)
...
- **%s 版本**: 待探测（SELECT version()）
...`, instance, displayName, displayName, ...)
```

**注意**:
- 修了模板只影响**新生成**的 PROFILE.md，**已存在**的 PROFILE.md 不会
  自动更新（LLM 后续 memory_update 时可能改一部分但不全）
- dbaa 用户如果已有 PROFILE.md，需手动跑：
  ```bash
  sed -i '' 's/openGauss/GaussDB/g' ~/.dbaa/memory/*/PROFILE.md
  ```
- 或者删掉 PROFILE.md 让 LLM 下次诊断时重新生成

**优先级**: P2（不影响功能，仅品牌一致性）

---

## 5. 其他几处硬编码 ~/.opendb 路径需 brand 化

**位置**:
- `internal/cluster/cmd.go:333-335`
- `internal/odberr/crash_log.go:107-109`
- `internal/opengauss/skill/monitor/trace.go:160`
- `internal/oracle/skill/monitor/trace.go:147`

**问题**: 这 4 处都直接写 `filepath.Join(home, ".opendb")`，不走 brand
层。dbaa build 触发这些代码路径时（cluster mode / 崩溃日志 / OS trace
工具）会写到 `~/.opendb/` 而不是 `~/.dbaa/`，造成隔离泄漏。

**修复方案**: 全部改成调 `config.DefaultOpenDBDir()`（已 brand 化）：
```go
import "github.com/sqlrush/opendb/internal/config"
// ...
baseDir := config.DefaultOpenDBDir()
return filepath.Join(baseDir, "trace")
```

**优先级**: P1（隔离漏点，影响 dbaa 数据隔离）

---

## 6. setup 向导里"OpenDB"残留扫荡

完成 v1.1.20 brand 改造后，建议全文 grep 一遍确认没有遗漏：
```bash
grep -rn "OpenDB\|opendb\|openGauss\|opengauss" internal/setup/ \
  | grep -v "import\|_test\|opendb/internal\|case \"opengauss\""
```

剩下的字面量都应该改成 `brand.Current().XXX`。

**优先级**: P1（清理残留品牌字样）
