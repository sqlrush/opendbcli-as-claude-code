/*-------------------------------------------------------------------------
 *
 * drift.go
 *	  DetectDrift returns true if newMessage has negligible topic
 *	  overlap with the most recent user message in history, meaning
 *	  resumed session state is probably stale and should be cleared.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/drift.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"strings"
	"unicode"
)

// TopicDriftThreshold is the Jaccard similarity below which a follow-up
// question is considered a topic change. Tuned empirically:
//   - 0.0 → no word overlap at all (definitely different topic)
//   - 0.1 → minor overlap (same domain words but different focus)
//   - 0.3 → moderate overlap (likely same topic)
const TopicDriftThreshold = 0.05

// DetectDrift returns true if newMessage has negligible topic overlap with
// the most recent user message in history, meaning resumed session state is
// probably stale and should be cleared.
//
// Empty history or empty message returns false (no drift — retain history).
func DetectDrift(history []Message, newMessage string) bool {
	prev := lastUserMessage(history)
	if prev == "" || newMessage == "" {
		return false
	}
	sim := jaccardSimilarity(tokenize(prev), tokenize(newMessage))
	return sim <= TopicDriftThreshold
}

// DropHistoryOnDrift returns history cleared if newMessage drifts from the
// last user turn, otherwise returns history unchanged.
func DropHistoryOnDrift(history []Message, newMessage string) []Message {
	if DetectDrift(history, newMessage) {
		return nil
	}
	return history
}

func lastUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == "user" && !m.IsMeta && m.Content != "" {
			return m.Content
		}
	}
	return ""
}

// tokenize splits text into a set of lowercase tokens.
// - ASCII letters/digits form whole words
// - Chinese CJK characters are split into individual characters (each as a "word")
// - Stopwords and length-1 English tokens are dropped
func tokenize(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var word strings.Builder
	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		w := strings.ToLower(word.String())
		word.Reset()
		if len(w) <= 1 {
			return
		}
		if _, stop := stopwords[w]; stop {
			return
		}
		tokens[w] = struct{}{}
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			if _, stop := cjkStopwords[string(r)]; !stop {
				tokens[string(r)] = struct{}{}
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		default:
			flushWord()
		}
	}
	flushWord()
	return tokens
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

var stopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "are": {}, "but": {}, "not": {}, "you": {},
	"all": {}, "can": {}, "this": {}, "that": {}, "with": {}, "from": {},
	"how": {}, "why": {}, "what": {}, "which": {}, "when": {}, "there": {},
	"select": {}, "where": {}, "from2": {},
}

// cjkStopwords are common Chinese function words that carry no topic info.
var cjkStopwords = map[string]struct{}{
	"的": {}, "是": {}, "在": {}, "了": {}, "和": {}, "与": {}, "有": {},
	"我": {}, "你": {}, "他": {}, "她": {}, "它": {}, "这": {}, "那": {},
	"吗": {}, "呢": {}, "啊": {}, "吧": {}, "也": {}, "都": {}, "就": {},
	"不": {}, "没": {}, "很": {}, "还": {}, "要": {}, "会": {}, "能": {},
	"什": {}, "么": {}, "怎": {}, "下": {}, "上": {}, "来": {}, "去": {},
	"给": {}, "把": {}, "被": {}, "让": {}, "为": {}, "以": {}, "到": {},
	"个": {}, "过": {}, "着": {}, "说": {}, "做": {}, "看": {}, "用": {},
}
