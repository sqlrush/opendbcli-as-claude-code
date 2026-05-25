package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type request struct {
	Params  map[string]any `json:"params"`
	Context runContext     `json:"context"`
}

type runContext struct {
	DBType     string `json:"db_type"`
	Connection string `json:"connection"`
	Database   string `json:"database"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
}

type hotspot struct {
	Line       string
	Node       string
	Object     string
	Cost       float64
	Rows       int64
	ActualRows int64
	Loops      int64
	Score      float64
	Reasons    []string
}

var (
	nodeRe   = regexp.MustCompile(`(?i)(Seq Scan|Index Scan|Index Only Scan|Bitmap Heap Scan|Bitmap Index Scan|Nested Loop|Hash Join|Merge Join|Sort|Aggregate|HashAggregate|GroupAggregate|WindowAgg|Materialize|Hash)`)
	costRe   = regexp.MustCompile(`cost=([0-9]+(?:\.[0-9]+)?)(?:\.\.([0-9]+(?:\.[0-9]+)?))?`)
	rowsRe   = regexp.MustCompile(`rows=([0-9]+)`)
	actualRe = regexp.MustCompile(`actual(?:_rows| rows)?=([0-9]+)`)
	loopsRe  = regexp.MustCompile(`loops=([0-9]+)`)
	onRe     = regexp.MustCompile(`(?i)\bon\s+([A-Za-z0-9_.$]+)`)
)

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fatal("invalid stdin JSON: " + err.Error())
	}
	plan := stringParam(req.Params, "plan_text")
	if strings.TrimSpace(plan) == "" {
		sql := stringParam(req.Params, "sql")
		if strings.TrimSpace(sql) == "" {
			usage()
			return
		}
		var err error
		plan, err = explainSQL(req, sql)
		if err != nil {
			softFail(err.Error())
			return
		}
	}
	topN := intParam(req.Params, "top_n", 8)
	if topN <= 0 || topN > 30 {
		topN = 8
	}
	hotspots := analyze(plan)
	rendered := render(req, plan, hotspots, topN)
	out := map[string]any{
		"ok":       true,
		"summary":  fmt.Sprintf("plan hotspots=%d", len(hotspots)),
		"rendered": rendered,
		"data": map[string]any{
			"hotspot_count": len(hotspots),
			"top_n":         topN,
		},
		"metadata": map[string]string{"source": "go_plan_hotspot_analyzer"},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func usage() {
	rendered := strings.TrimSpace(strings.Join([]string{
		"# Go Plan Hotspot Analyzer",
		"",
		"需要传入以下任一参数：",
		"",
		"- plan_text：直接分析一段 EXPLAIN/EXPLAIN PERFORMANCE 文本",
		"- sql：由 skill 调用数据库客户端执行 EXPLAIN 后再分析",
		"",
		"示例：",
		"",
		"```text",
		`/skills run go_plan_hotspot_analyzer {"plan_text":"Seq Scan on t cost=10000 rows=1\nSort cost=20000 rows=1","top_n":3}`,
		"```",
		"",
		"说明：当前 P0 script skill 是外部进程边界，若传 sql 参数需要客户沙箱内存在 gsql/psql；后续应接入 DBAA 内部 SQL bridge，避免外部脚本自行连接数据库。",
	}, "\n"))
	out := map[string]any{
		"ok":       true,
		"summary":  "plan_text or sql is required",
		"rendered": rendered,
		"metadata": map[string]string{"source": "go_plan_hotspot_analyzer"},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func softFail(msg string) {
	out := map[string]any{
		"ok":       false,
		"summary":  msg,
		"rendered": "go_plan_hotspot_analyzer 执行失败：" + msg,
		"metadata": map[string]string{"source": "go_plan_hotspot_analyzer"},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func fatal(msg string) {
	out := map[string]any{
		"ok":       false,
		"summary":  msg,
		"rendered": "go_plan_hotspot_analyzer 执行失败：" + msg,
		"metadata": map[string]string{"source": "go_plan_hotspot_analyzer"},
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
	os.Exit(1)
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func intParam(params map[string]any, key string, fallback int) int {
	if params == nil {
		return fallback
	}
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func explainSQL(req request, sql string) (string, error) {
	cli := stringParam(req.Params, "dbcli")
	if cli == "" {
		cli = findClient()
	}
	if cli == "" {
		return "", fmt.Errorf("PATH 中未找到 gsql 或 psql；请传 plan_text，或在客户沙箱安装数据库客户端")
	}
	host := defaultString(req.Context.Host, "127.0.0.1")
	port := req.Context.Port
	if port == 0 {
		port = 5432
	}
	db := defaultString(req.Context.Database, "postgres")
	user := defaultString(req.Context.User, "omm")
	explain := "EXPLAIN " + strings.TrimRight(sql, " ;")
	base := baseName(cli)
	var cmd *exec.Cmd
	if base == "gsql" {
		cmd = exec.Command(cli, "-X", "-q", "-h", host, "-p", strconv.Itoa(port), "-d", db, "-U", user, "-c", explain)
	} else {
		cmd = exec.Command(cli, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-h", host, "-p", strconv.Itoa(port), "-d", db, "-U", user, "-c", explain)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s EXPLAIN failed: %s", base, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func findClient() string {
	for _, c := range []string{"gsql", "psql"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func baseName(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func analyze(plan string) []hotspot {
	lines := strings.Split(plan, "\n")
	var out []hotspot
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimLeft(raw, "|`-+ "))
		if line == "" {
			continue
		}
		node := first(nodeRe.FindStringSubmatch(line))
		if node == "" {
			continue
		}
		hs := hotspot{Line: line, Node: canonicalNode(node), Object: objectName(line), Loops: 1}
		if m := costRe.FindStringSubmatch(line); len(m) > 0 {
			hs.Cost = parseFloat(firstNonEmpty(m[2], m[1]))
		}
		if m := rowsRe.FindStringSubmatch(line); len(m) > 1 {
			hs.Rows = parseInt64(m[1])
		}
		if m := actualRe.FindStringSubmatch(line); len(m) > 1 {
			hs.ActualRows = parseInt64(m[1])
		}
		if m := loopsRe.FindStringSubmatch(line); len(m) > 1 {
			hs.Loops = parseInt64(m[1])
		}
		if i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(next, "Filter:") || strings.HasPrefix(next, "Index Cond:") || strings.HasPrefix(next, "Hash Cond:") || strings.HasPrefix(next, "Join Filter:") {
				hs.Line += " | " + next
			}
		}
		scoreHotspot(&hs)
		if len(hs.Reasons) > 0 {
			out = append(out, hs)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func first(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func canonicalNode(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "HashAggregate") {
		return "HashAggregate"
	}
	return strings.Title(strings.ToLower(s))
}

func objectName(line string) string {
	m := onRe.FindStringSubmatch(line)
	if len(m) > 1 {
		return m[1]
	}
	return "-"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return "0"
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func scoreHotspot(h *hotspot) {
	h.Score = h.Cost / 1000.0
	lower := strings.ToLower(h.Node)
	switch {
	case strings.Contains(lower, "seq scan"):
		if h.Cost >= 1000 || h.Rows >= 10000 {
			h.Score += 30
			h.Reasons = append(h.Reasons, "顺序扫描: 检查过滤列/连接列索引、统计信息和分区裁剪")
		}
	case strings.Contains(lower, "nested loop"):
		if h.Cost >= 10000 || h.Rows >= 1000 {
			h.Score += 25
			h.Reasons = append(h.Reasons, "Nested Loop 成本高: 检查内层索引、驱动表行数和 join 顺序")
		}
	case strings.Contains(lower, "sort"):
		if h.Cost >= 5000 {
			h.Score += 18
			h.Reasons = append(h.Reasons, "排序成本高: 检查 ORDER BY/LIMIT 下推、排序键索引或 work_mem")
		}
	case strings.Contains(lower, "hash join"), strings.EqualFold(h.Node, "Hash"):
		if h.Cost >= 10000 {
			h.Score += 14
			h.Reasons = append(h.Reasons, "Hash 算子成本高: 检查构建端行数、work_mem 和连接列统计")
		}
	case strings.Contains(lower, "aggregate"), strings.Contains(lower, "window"):
		if h.Cost >= 10000 {
			h.Score += 12
			h.Reasons = append(h.Reasons, "聚合/窗口成本高: 检查分组列基数、预聚合和排序路径")
		}
	case strings.Contains(lower, "materialize"):
		h.Score += 10
		h.Reasons = append(h.Reasons, "Materialize: 检查重复扫描、CTE 物化和内存占用")
	}
	if h.ActualRows > 0 && h.Rows > 0 {
		ratio := float64(h.ActualRows) / float64(h.Rows)
		if ratio >= 10 || ratio <= 0.1 {
			h.Score += 20
			h.Reasons = append(h.Reasons, fmt.Sprintf("行数估算偏差 %.1fx: 优先 ANALYZE/扩展统计/直方图", ratio))
		}
	}
	if strings.Contains(strings.ToLower(h.Line), "filter:") && strings.Contains(strings.ToLower(h.Node), "seq scan") {
		h.Score += 8
		h.Reasons = append(h.Reasons, "Seq Scan 带 Filter: 过滤列可能缺少合适索引或谓词选择性被低估")
	}
}

func render(req request, plan string, hotspots []hotspot, topN int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan Hotspot Analyzer\n\n")
	fmt.Fprintf(&b, "- connection: %s\n", defaultString(req.Context.Connection, "-"))
	fmt.Fprintf(&b, "- db_type: %s\n", defaultString(req.Context.DBType, "-"))
	fmt.Fprintf(&b, "- plan_lines: %d\n", len(strings.Split(plan, "\n")))
	fmt.Fprintf(&b, "- hotspot_count: %d\n\n", len(hotspots))
	fmt.Fprintf(&b, "## 1. Top Hotspots\n\n")
	if len(hotspots) == 0 {
		fmt.Fprintf(&b, "未识别到明显热点。请确认输入是否为 EXPLAIN 文本。\n\n")
	} else {
		fmt.Fprintf(&b, "|#|node|object|cost|rows|actual_rows|score|reason|\n")
		fmt.Fprintf(&b, "|---|---|---|---:|---:|---:|---:|---|\n")
		limit := topN
		if limit > len(hotspots) {
			limit = len(hotspots)
		}
		for i := 0; i < limit; i++ {
			h := hotspots[i]
			fmt.Fprintf(&b, "|%d|%s|%s|%.0f|%d|%d|%.1f|%s|\n", i+1, h.Node, h.Object, h.Cost, h.Rows, h.ActualRows, h.Score, strings.Join(h.Reasons, "; "))
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## 2. DBA Actions\n\n")
	fmt.Fprintf(&b, "- Seq Scan 热点: 先核对谓词列/连接列索引、表大小、统计信息新鲜度和分区裁剪。\n")
	fmt.Fprintf(&b, "- Nested Loop 热点: 检查内层扫描是否按外层行数重复放大，必要时补内层连接列索引或改 join 顺序。\n")
	fmt.Fprintf(&b, "- Sort/Hash 热点: 先用会话级 work_mem 复测，不要直接全局调大。\n")
	fmt.Fprintf(&b, "- 行数估算偏差: 优先 ANALYZE、提高 statistics target、补多列扩展统计。\n")
	fmt.Fprintf(&b, "\n## Safety Notes\n\n")
	fmt.Fprintf(&b, "- 本 skill 只读分析 plan 或执行 EXPLAIN，不执行 EXPLAIN ANALYZE。\n")
	fmt.Fprintf(&b, "- 输出是热点方向，不直接代表可落库变更；索引/统计/参数建议仍需 SQLTune 或 DBA 复核。\n")
	return b.String()
}
