/*-------------------------------------------------------------------------
 *
 * json_types.go
 *	  JSONRule is the top-level JSON rule structure.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/json_types.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"encoding/json"
	"strings"
)

// ─── JSON Rule Schema ──────────────────────────────────────────────────────
//
// These types mirror the ailinkdb JSON rule format exactly.
// They are intermediate deserialization types — converted to *Rule by json_parser.go.

// JSONRule is the top-level JSON rule structure.
type JSONRule struct {
	RuleID       string                   `json:"rule_id"`
	Name         string                   `json:"name"`
	Category     string                   `json:"category"`
	Subcategory  string                   `json:"subcategory"`
	Database     string                   `json:"database"`
	Versions     string                   `json:"versions"`
	Tags         []string                 `json:"tags"`
	Description  string                   `json:"description"`
	Signals      []JSONSignal             `json:"signals"`
	Trigger      JSONTrigger              `json:"trigger"`
	RootCauses   []JSONRootCause          `json:"root_causes"`
	Thresholds   map[string]interface{}   `json:"thresholds"`
	DiagQueries  map[string]JSONDiagQuery `json:"diagnostic_queries"`
	Tree         *JSONTreeNode            `json:"decision_tree"`
	Related      JSONRelatedRules         `json:"related_rules"`
	VersionNotes map[string]string        `json:"version_specific_notes"`
}

// JSONSignal is a signal declaration in a JSON rule.
type JSONSignal struct {
	Type string `json:"type"`
	Key  string `json:"key"`
	Desc string `json:"desc"`
}

// JSONTrigger defines activation conditions.
type JSONTrigger struct {
	Mode       string          `json:"mode"`
	Conditions []JSONCondition `json:"conditions"`
	SkipWhen   []JSONSkipWhen  `json:"skip_when"`
}

// JSONCondition is a single trigger predicate.
type JSONCondition struct {
	Source string      `json:"source"`
	Field  string      `json:"field"`
	Op     string      `json:"op"`
	Value  interface{} `json:"value"` // float64, string, or bool from JSON
	Desc   string      `json:"desc"`
}

// JSONSkipWhen defines when a rule should not activate.
type JSONSkipWhen struct {
	Desc      string `json:"desc"`
	Condition string `json:"condition"` // expression string like "threads_running < 1"
}

// JSONRootCause is a potential root cause definition.
type JSONRootCause struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Probability string `json:"probability"` // "35%"
	Desc        string `json:"desc"`
}

// JSONDiagQuery is a named diagnostic SQL query.
type JSONDiagQuery struct {
	Desc     string `json:"desc"`
	SQL      string `json:"sql"`
	Database string `json:"database"` // optional, for cross-DB rules
}

// JSONTreeNode is a decision tree node.
type JSONTreeNode struct {
	Step              string       `json:"step"`
	Query             string       `json:"query"` // reference to diagnostic_queries key
	EliminationMethod bool         `json:"elimination_method"`
	Branches          []JSONBranch `json:"branches"`
}

// JSONBranch is a branch in the decision tree.
type JSONBranch struct {
	Label          string         `json:"label"`
	Condition      JSONBranchCond `json:"condition"` // polymorphic: object or "default"
	Then           *JSONTreeNode  `json:"then"`
	Severity       string         `json:"severity"`
	RootCause      string         `json:"root_cause"` // "RC1", "RC2", etc.
	Confidence     string         `json:"confidence"`  // "置信度 90%: 5个独立证据"
	Findings       []string       `json:"findings"`
	Actions        []JSONAction   `json:"actions"`
	Verification   []string       `json:"verification"`
	CrossReference jsonCrossRef   `json:"cross_reference"` // polymorphic: object, array, or string
}

// JSONAction is a remediation action.
type JSONAction struct {
	Type     string `json:"type"`     // "fix", "urgent", "investigate", etc.
	Desc     string `json:"desc"`
	SQL      string `json:"sql"`
	Risk     string `json:"risk"`
	Rollback string `json:"rollback"`
}

// JSONRelatedRules defines relationships to other rules.
type JSONRelatedRules struct {
	CausedBy jsonRelationList `json:"caused_by"`
	CausesOf jsonRelationList `json:"causes_of"`
	Related  jsonRelationList `json:"related"`
}

// JSONRelation is a single rule relationship.
type JSONRelation struct {
	RuleID       string `json:"rule_id"`
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
}

// jsonRelationList handles polymorphic relation arrays.
type jsonRelationList []JSONRelation

// UnmarshalJSON handles the polymorphic relation array.
func (rl *jsonRelationList) UnmarshalJSON(data []byte) error {
	var objs []JSONRelation
	if err := json.Unmarshal(data, &objs); err == nil {
		*rl = objs
		return nil
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	result := make([]JSONRelation, 0, len(raw))
	for _, item := range raw {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			result = append(result, JSONRelation{RuleID: s})
			continue
		}
		var obj JSONRelation
		if err := json.Unmarshal(item, &obj); err == nil {
			result = append(result, obj)
		}
	}
	*rl = result
	return nil
}

// ─── Polymorphic Unmarshaling ──────────────────────────────────────────────

// JSONBranchCond handles polymorphic "condition" field.
type JSONBranchCond struct {
	IsDefault bool
	Field     string
	Op        string
	Value     interface{}
}

// UnmarshalJSON handles the polymorphic condition field.
func (c *JSONBranchCond) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if strings.ToLower(s) == "default" {
			c.IsDefault = true
			return nil
		}
		c.Field = s
		c.Op = "exists"
		return nil
	}

	var obj struct {
		Field string      `json:"field"`
		Op    string      `json:"op"`
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		c.Field = obj.Field
		c.Op = NormalizeOp(obj.Op)
		c.Value = obj.Value
		return nil
	}

	c.IsDefault = true
	return nil
}

// jsonCrossRef handles polymorphic cross_reference field.
type jsonCrossRef struct {
	Refs []CrossRefEntry
}

// CrossRefEntry is a single cross-reference.
type CrossRefEntry struct {
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
	Desc   string `json:"desc"`
}

// UnmarshalJSON handles the polymorphic cross_reference field.
func (cr *jsonCrossRef) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		cr.Refs = []CrossRefEntry{{RuleID: s}}
		return nil
	}

	var arr []CrossRefEntry
	if err := json.Unmarshal(data, &arr); err == nil {
		cr.Refs = arr
		return nil
	}

	var single CrossRefEntry
	if err := json.Unmarshal(data, &single); err == nil {
		cr.Refs = []CrossRefEntry{single}
		return nil
	}

	var strArr []string
	if err := json.Unmarshal(data, &strArr); err == nil {
		for _, id := range strArr {
			cr.Refs = append(cr.Refs, CrossRefEntry{RuleID: id})
		}
		return nil
	}

	return nil
}
