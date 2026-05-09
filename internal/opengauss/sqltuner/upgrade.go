/*-------------------------------------------------------------------------
 *
 * upgrade.go
 *	  UpgradeDecision describes whether to upgrade and why.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/upgrade.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/llm"
)

// 自动升级机制 (design doc §5)
//
// Round 1 完成后, tuner 自检 4 个信号. 命中任一 → 升级到深度模式 (3-15 轮迭代).
//
// 升级信号:
//   ❶ LLM 自报低置信      confidence < 0.7
//   ❷ 改善幅度不足        所有 candidate 验证后 min(new/old) > 0.5 (没找到 ≥2× 改善)
//   ❸ 探索方向单一        len(explored_dimensions) < 3
//   ❹ SQL 复杂度超阈     SQL > 500 行 OR 涉及表 > 20 OR plan 节点 > 100

// UpgradeDecision describes whether to upgrade and why.
type UpgradeDecision struct {
	ShouldUpgrade bool
	Reasons       []string
	BestSpeedup   float64
}

// EvaluateUpgrade checks the 4 signals against Round 1 output + verification results.
func EvaluateUpgrade(cc *CollectedContext, round1 *Round1Output, verifies []VerifyResult) UpgradeDecision {
	d := UpgradeDecision{}

	// Signal 1: low confidence
	if round1.Confidence < 0.7 {
		d.ShouldUpgrade = true
		d.Reasons = append(d.Reasons, fmt.Sprintf("LLM 自报置信度 %.2f < 0.7", round1.Confidence))
	}

	// Signal 2: no candidate achieves ≥ 2× speedup
	bestSpeedup := 0.0
	for _, vr := range verifies {
		if vr.Verifiable && vr.Error == "" && vr.Speedup > bestSpeedup {
			bestSpeedup = vr.Speedup
		}
	}
	d.BestSpeedup = bestSpeedup
	if bestSpeedup < 2.0 && bestSpeedup > 0 {
		// Only flag if some candidates were verifiable (avoid flagging when all are DDL)
		hasVerifiable := false
		for _, vr := range verifies {
			if vr.Verifiable {
				hasVerifiable = true
				break
			}
		}
		if hasVerifiable {
			d.ShouldUpgrade = true
			d.Reasons = append(d.Reasons, fmt.Sprintf("最佳 candidate 改善仅 %.1f× (< 2×)", bestSpeedup))
		}
	}

	// Signal 3: exploration too narrow
	if len(round1.ExploredDimensions) < 3 {
		d.ShouldUpgrade = true
		d.Reasons = append(d.Reasons, fmt.Sprintf("探索维度仅 %d 个 (< 3)", len(round1.ExploredDimensions)))
	}

	// Signal 4: SQL complexity threshold (catches千行 SQL even before Round 1 result)
	complexityReason := ""
	lines := strings.Count(cc.OrigSQL, "\n") + 1
	if lines > 500 {
		complexityReason = fmt.Sprintf("SQL %d 行 > 500", lines)
	} else if len(cc.InvolvedTables) > 20 {
		complexityReason = fmt.Sprintf("涉及表 %d 个 > 20", len(cc.InvolvedTables))
	} else if cc.Plan != nil && countPlanNodes(cc.Plan.Root) > 100 {
		complexityReason = fmt.Sprintf("plan 节点 %d > 100", countPlanNodes(cc.Plan.Root))
	}
	if complexityReason != "" {
		d.ShouldUpgrade = true
		d.Reasons = append(d.Reasons, "复杂度超阈: "+complexityReason)
	}

	return d
}

// runDeepMode performs additional rounds of LLM-driven refinement when upgrade is triggered.
//
// Each round:
//   1. Show LLM previous candidates + verification results
//   2. Ask LLM to propose NEW candidates (not retrying same ideas)
//   3. Verify new candidates (parallel EXPLAIN)
//   4. Repeat until convergence or maxRounds
//
// Convergence:
//   - At least 1 candidate achieves cost < 20% of original (5×+ speedup)
//   - OR 3 consecutive rounds with no improvement
//   - OR maxRounds reached
func (t *Tuner) runDeepMode(
	ctx context.Context,
	cc *CollectedContext,
	round1 *Round1Output,
	initialVerifies []VerifyResult,
	maxRounds int,
) (*Round1Output, []VerifyResult, int, error) {
	allCandidates := append([]Candidate(nil), round1.Candidates...)
	allVerifies := append([]VerifyResult(nil), initialVerifies...)
	bestSpeedup := 0.0
	for _, v := range initialVerifies {
		if v.Speedup > bestSpeedup {
			bestSpeedup = v.Speedup
		}
	}

	noImproveStreak := 0
	rounds := 0
	for round := 0; round < maxRounds; round++ {
		rounds++
		// Convergence check
		if bestSpeedup >= 5.0 {
			break // already found 5×+ improvement
		}
		if noImproveStreak >= 3 {
			break // stuck
		}

		newCands, err := t.runDeepRound(ctx, cc, round1, allCandidates, allVerifies)
		if err != nil {
			return round1, allVerifies, rounds, err
		}
		if len(newCands) == 0 {
			noImproveStreak++
			continue
		}

		// Renumber to avoid id collision
		nextID := len(allCandidates) + 1
		for i := range newCands {
			newCands[i].ID = nextID + i
		}

		newVerifies := t.verifyCandidates(ctx, cc.OrigSQL, newCands, true)
		allCandidates = append(allCandidates, newCands...)
		allVerifies = append(allVerifies, newVerifies...)

		// Track improvement
		roundBest := 0.0
		for _, v := range newVerifies {
			if v.Speedup > roundBest {
				roundBest = v.Speedup
			}
		}
		if roundBest > bestSpeedup {
			bestSpeedup = roundBest
			noImproveStreak = 0
		} else {
			noImproveStreak++
		}
	}

	// Update round1 with merged candidates so Round 2 sees the full set
	round1.Candidates = allCandidates
	return round1, allVerifies, rounds, nil
}

// runDeepRound asks the LLM for NEW candidates given the history.
func (t *Tuner) runDeepRound(
	ctx context.Context,
	cc *CollectedContext,
	round1 *Round1Output,
	prevCandidates []Candidate,
	prevVerifies []VerifyResult,
) ([]Candidate, error) {
	systemPrompt := `你是 OG SQL 调优专家. 现在是深度迭代模式 — 用户已经收到了第一轮的方案,
但效果不够好. 请基于已有方案的验证结果, 提出**全新方向的方案**.

要求:
- 不要重复已有方案的思路
- 看验证结果学习: 哪些方向不 work, 换思路
- 探索更激进的优化 (例如表结构改造 / 大幅 SQL 重写 / 多 hint 组合)
- 输出严格 JSON, 格式同 Round 1 (但只包含 candidates 字段即可)
- 给 2-4 个新方案

输出格式:
{
  "candidates": [
    {"id": 0, "type": "...", "sql": "...", "rationale": "...", "expected_gain": "...", "applies_to": [...], "risk_level": "..."}
  ],
  "uncertainty_notes": ["..."]
}
`

	var b strings.Builder
	b.WriteString("# 已尝试的方案及验证结果\n\n")
	for _, c := range prevCandidates {
		b.WriteString(fmt.Sprintf("## Cand %d (%s, risk=%s)\n", c.ID, c.Type, c.RiskLevel))
		b.WriteString("Rationale: " + c.Rationale + "\n")
		b.WriteString("```sql\n" + c.SQL + "\n```\n")
		// Find matching verify
		for _, v := range prevVerifies {
			if v.CandID == c.ID {
				if v.Verifiable && v.Error == "" {
					b.WriteString(fmt.Sprintf("**验证**: cost %.0f → %.0f (%.1f×)\n", v.OldCost, v.NewCost, v.Speedup))
				} else if !v.Verifiable {
					b.WriteString("**验证**: DDL 类未实跑\n")
				} else {
					b.WriteString("**验证**: ❌ " + v.Error + "\n")
				}
				break
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n# 原 SQL\n```sql\n" + cc.OrigSQL + "\n```\n\n")
	if cc.Plan != nil {
		b.WriteString(fmt.Sprintf("原 cost: %.2f\n", cc.Plan.TotalCost))
	}
	b.WriteString("\n请基于以上, 给出 2-4 个**全新方向**的方案 JSON.\n")

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: b.String()},
		},
		MaxTokens: 4000,
	}

	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	resp, err := t.provider.Chat(cctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Content == "" {
		return nil, fmt.Errorf("empty deep-round response")
	}

	// Parse - tolerate just a candidates array or full object
	out, err := ParseRound1Output(resp.Content)
	if err != nil {
		// Could be just {"candidates": [...]} - retry with stricter prompt
		return nil, fmt.Errorf("parse deep-round JSON: %w", err)
	}
	return out.Candidates, nil
}
