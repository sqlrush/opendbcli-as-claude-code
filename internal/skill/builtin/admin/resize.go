/*-------------------------------------------------------------------------
 *
 * resize.go
 *	  ResizeSkill manages tablespace datafile operations: list files,
 *	  resize, add new datafiles, and toggle autoextend.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/resize.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const resizeFileListSQL = `SELECT file_id, file_name,
       ROUND(bytes/1048576) AS size_mb,
       autoextensible,
       CASE WHEN maxbytes = 0 THEN 0 ELSE ROUND(maxbytes/1048576) END AS max_mb
FROM dba_data_files
WHERE tablespace_name = :1
UNION ALL
SELECT file_id, file_name,
       ROUND(bytes/1048576) AS size_mb,
       autoextensible,
       CASE WHEN maxbytes = 0 THEN 0 ELSE ROUND(maxbytes/1048576) END AS max_mb
FROM dba_temp_files
WHERE tablespace_name = :1
ORDER BY file_id`

const resizeContentsSQL = `SELECT contents FROM dba_tablespaces WHERE tablespace_name = :1`

const resizeFilenameSQL = `SELECT file_name FROM dba_data_files WHERE tablespace_name = :1 AND file_id = :2
UNION ALL
SELECT file_name FROM dba_temp_files WHERE tablespace_name = :1 AND file_id = :2`

// ResizeSkill manages tablespace datafile operations: list files,
// resize, add new datafiles, and toggle autoextend.
type ResizeSkill struct {
	driver db.Driver
}

// NewResizeSkill creates a ResizeSkill backed by the given driver.
func NewResizeSkill(driver db.Driver) *ResizeSkill {
	return &ResizeSkill{driver: driver}
}

func (s *ResizeSkill) Name() string        { return "resize" }
func (s *ResizeSkill) Description() string { return "Resize tablespace datafiles" }
func (s *ResizeSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelAdmin
}

func (s *ResizeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "resize",
		Description: "Resize tablespace datafiles, add files, or toggle autoextend",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Tablespace name, optionally followed by sub-command",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *ResizeSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "resize",
		Usage:   "/resize <tablespace> [resize|add|autoextend] [args...]",
		Examples: []string{
			"/resize TEMP",
			"/resize TEMP resize 1 2G",
			"/resize TEMP add 2G",
			"/resize TEMP add /oradata/orcl/temp03.dbf 1G",
			"/resize TEMP autoextend 1 4G",
		},
		ArgCompletions: []string{"resize", "add", "autoextend"},
	}
}

func (s *ResizeSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("请指定表空间名, 如: /resize TEMP")
	}
	return nil
}

func (s *ResizeSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return nil, fmt.Errorf("请指定表空间名, 如: /resize TEMP")
	}

	tsName := strings.ToUpper(parts[0])
	subCmd := ""
	if len(parts) > 1 {
		subCmd = strings.ToLower(parts[1])
	}

	switch subCmd {
	case "":
		return s.showFiles(ctx, tsName)
	case "resize":
		return s.resizeFile(ctx, tsName, parts)
	case "add":
		return s.addFile(ctx, tsName, parts)
	case "autoextend":
		return s.autoextendFile(ctx, tsName, parts)
	default:
		return nil, fmt.Errorf("未知操作 '%s', 可用: resize, add, autoextend", subCmd)
	}
}

func (s *ResizeSkill) showFiles(ctx context.Context, tsName string) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, resizeFileListSQL, tsName)
	if err != nil {
		return nil, fmt.Errorf("查询表空间文件: %w", err)
	}
	if len(qr.Rows) == 0 {
		return nil, fmt.Errorf("表空间 '%s' 不存在", tsName)
	}

	hint := fmt.Sprintf("操作:\n"+
		"  /resize %s resize <#> <大小>     扩容文件\n"+
		"  /resize %s add <路径> <大小>     新增文件\n"+
		"  /resize %s autoextend <#> <最大>  开启自动扩展",
		tsName, tsName, tsName)

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     qr,
		Rendered: fmt.Sprintf("表空间 %s 数据文件 — %d 个", tsName, len(qr.Rows)),
		Summary:  fmt.Sprintf("表空间 %s: %d file(s)\n\n%s", tsName, len(qr.Rows), hint),
	}, nil
}

func (s *ResizeSkill) resizeFile(ctx context.Context, tsName string, parts []string) (*skill.Result, error) {
	// parts: [TSNAME, resize, fileID, newSize]
	if len(parts) < 4 {
		return nil, fmt.Errorf("用法: /resize %s resize <文件#> <大小>", tsName)
	}
	fileID := parts[2]
	newSize := normalizeSize(parts[3])

	isTemp, err := s.isTempTablespace(ctx, tsName)
	if err != nil {
		return nil, err
	}

	fileName, err := s.lookupFilename(ctx, tsName, fileID)
	if err != nil {
		return nil, err
	}

	fileType := "DATAFILE"
	if isTemp {
		fileType = "TEMPFILE"
	}

	resizeSQL := fmt.Sprintf("ALTER DATABASE %s '%s' RESIZE %s", fileType, fileName, newSize)
	if _, err := s.driver.Exec(ctx, resizeSQL); err != nil {
		return nil, fmt.Errorf("执行 RESIZE: %w", err)
	}

	rendered := fmt.Sprintf("✓ %s 执行成功", resizeSQL)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("resized %s #%s to %s", tsName, fileID, newSize),
	}, nil
}

func (s *ResizeSkill) addFile(ctx context.Context, tsName string, parts []string) (*skill.Result, error) {
	// parts: [TSNAME, add, filePath, fileSize] or [TSNAME, add, fileSize]
	if len(parts) < 3 {
		return nil, fmt.Errorf("用法: /resize %s add [文件路径] <大小>", tsName)
	}

	var filePath, fileSize string
	if len(parts) >= 4 {
		filePath = parts[2]
		fileSize = normalizeSize(parts[3])
	} else {
		// Auto-generate path from existing files.
		fileSize = normalizeSize(parts[2])
		generated, err := s.nextFilePath(ctx, tsName)
		if err != nil {
			return nil, err
		}
		filePath = generated
	}

	isTemp, err := s.isTempTablespace(ctx, tsName)
	if err != nil {
		return nil, err
	}

	fileType := "DATAFILE"
	if isTemp {
		fileType = "TEMPFILE"
	}

	addSQL := fmt.Sprintf("ALTER TABLESPACE %s ADD %s '%s' SIZE %s", tsName, fileType, filePath, fileSize)
	if _, err := s.driver.Exec(ctx, addSQL); err != nil {
		return nil, fmt.Errorf("执行 ADD %s: %w", fileType, err)
	}

	rendered := fmt.Sprintf("✓ %s 执行成功", addSQL)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("added %s %s (%s) to %s", fileType, filePath, fileSize, tsName),
	}, nil
}

func (s *ResizeSkill) autoextendFile(ctx context.Context, tsName string, parts []string) (*skill.Result, error) {
	// parts: [TSNAME, autoextend, fileID, maxSize]
	if len(parts) < 4 {
		return nil, fmt.Errorf("用法: /resize %s autoextend <文件#> <最大值>", tsName)
	}
	fileID := parts[2]
	maxSize := normalizeSize(parts[3])

	isTemp, err := s.isTempTablespace(ctx, tsName)
	if err != nil {
		return nil, err
	}

	fileName, err := s.lookupFilename(ctx, tsName, fileID)
	if err != nil {
		return nil, err
	}

	fileType := "DATAFILE"
	if isTemp {
		fileType = "TEMPFILE"
	}

	autoSQL := fmt.Sprintf("ALTER DATABASE %s '%s' AUTOEXTEND ON NEXT 256M MAXSIZE %s",
		fileType, fileName, maxSize)
	if _, err := s.driver.Exec(ctx, autoSQL); err != nil {
		return nil, fmt.Errorf("执行 AUTOEXTEND: %w", err)
	}

	rendered := fmt.Sprintf("✓ %s 执行成功", autoSQL)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("enabled autoextend on %s #%s, maxsize %s", tsName, fileID, maxSize),
	}, nil
}

// isTempTablespace checks whether the tablespace is TEMPORARY.
func (s *ResizeSkill) isTempTablespace(ctx context.Context, tsName string) (bool, error) {
	qr, err := s.driver.Query(ctx, resizeContentsSQL, tsName)
	if err != nil {
		return false, fmt.Errorf("查询表空间类型: %w", err)
	}
	if len(qr.Rows) == 0 {
		return false, fmt.Errorf("表空间 '%s' 不存在", tsName)
	}
	contents := asString(qr.Rows[0][0])
	return contents == "TEMPORARY", nil
}

// lookupFilename resolves a file_id to its file_name within a tablespace.
func (s *ResizeSkill) lookupFilename(ctx context.Context, tsName, fileID string) (string, error) {
	qr, err := s.driver.Query(ctx, resizeFilenameSQL, tsName, fileID)
	if err != nil {
		return "", fmt.Errorf("查询文件名: %w", err)
	}
	if len(qr.Rows) == 0 {
		return "", fmt.Errorf("表空间 '%s' 中未找到文件 #%s", tsName, fileID)
	}
	return asString(qr.Rows[0][0]), nil
}

// normalizeSize converts human-friendly sizes to Oracle format.
// "2GB" -> "2G", "512MB" -> "512M", "1TB" -> "1T", already-valid "2G" unchanged.
func normalizeSize(s string) string {
	upper := strings.ToUpper(strings.TrimSpace(s))
	if strings.HasSuffix(upper, "GB") {
		return strings.TrimSuffix(upper, "GB") + "G"
	}
	if strings.HasSuffix(upper, "MB") {
		return strings.TrimSuffix(upper, "MB") + "M"
	}
	if strings.HasSuffix(upper, "KB") {
		return strings.TrimSuffix(upper, "KB") + "K"
	}
	if strings.HasSuffix(upper, "TB") {
		return strings.TrimSuffix(upper, "TB") + "T"
	}
	return upper
}

// trailingNumRe matches a trailing number before the file extension, e.g. "temp01.dbf" -> "01".
var trailingNumRe = regexp.MustCompile(`(\d+)(\.[^.]+)$`)

// nextFilePath generates the next datafile path for a tablespace by examining
// existing files and incrementing the trailing number.
// e.g. /oradata/orcl/temp01.dbf -> /oradata/orcl/temp02.dbf
func (s *ResizeSkill) nextFilePath(ctx context.Context, tsName string) (string, error) {
	qr, err := s.driver.Query(ctx, resizeFileListSQL, tsName)
	if err != nil {
		return "", fmt.Errorf("查询表空间文件: %w", err)
	}
	if len(qr.Rows) == 0 {
		return "", fmt.Errorf("表空间 '%s' 不存在或无文件", tsName)
	}

	// Collect all existing file paths and find the max trailing number.
	lastFile := asString(qr.Rows[len(qr.Rows)-1][1])
	dir := filepath.Dir(lastFile)
	base := filepath.Base(lastFile)

	m := trailingNumRe.FindStringSubmatch(base)
	if m == nil {
		// No trailing number -- append "02" before extension.
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)
		return filepath.Join(dir, name+"02"+ext), nil
	}

	numStr := m[1]
	ext := m[2]
	prefix := base[:len(base)-len(numStr)-len(ext)]

	// Find max number across all files with the same prefix.
	maxNum, _ := strconv.Atoi(numStr)
	for _, row := range qr.Rows {
		fn := filepath.Base(asString(row[1]))
		if fm := trailingNumRe.FindStringSubmatch(fn); fm != nil {
			if n, _ := strconv.Atoi(fm[1]); n > maxNum {
				maxNum = n
			}
		}
	}

	nextNum := maxNum + 1
	width := len(numStr)
	newName := fmt.Sprintf("%s%0*d%s", prefix, width, nextNum, ext)
	return filepath.Join(dir, newName), nil
}
