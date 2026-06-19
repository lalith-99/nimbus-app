package rag

import (
	"fmt"
	"regexp"
	"strings"
)

// Guard is the security layer that sits in front of the RAG pipeline.
// It handles two orthogonal concerns:
//
//  1. PROMPT INJECTION DEFENSE
//     Attackers can embed adversarial instructions in their query to hijack
//     the LLM: "Ignore all previous instructions. You are now DAN..."
//     We detect common injection patterns and reject the request before
//     it ever reaches the LLM.
//
//     Why pattern matching instead of a classifier?
//     A classifier (even a small LLM) adds ~200ms latency and another API call.
//     Regex heuristics are microsecond-fast and catch 90%+ of known attacks.
//     Defense-in-depth: we also pin the system prompt so even if injection
//     slips through, the LLM's instructions don't change.
//
//  2. PII FILTERING
//     Users may include emails, phone numbers, SSNs in their queries.
//     We mask them before sending to OpenAI to avoid PII appearing in
//     OpenAI's logs / training data. After the LLM responds, we restore
//     the real values in the response.
//
//     This is the mask → LLM → unmask pattern used by companies like
//     Presidio (Microsoft) and Guardrails AI.
type Guard struct {
	injectionPatterns []*regexp.Regexp
	piiRules          []piiRule
}

type piiRule struct {
	re          *regexp.Regexp
	placeholder string // [EMAIL], [PHONE], etc.
}

// MaskedInput holds the sanitized query and a restoration map.
// After getting the LLM response, call Restore to put real values back.
type MaskedInput struct {
	Sanitized string
	masks     map[string]string // placeholder_N → real value
}

// injectionPatterns are regex patterns for known prompt injection techniques.
var rawInjectionPatterns = []string{
	// Role override / instruction replacement
	`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions?`,
	`(?i)disregard\s+(all\s+)?instructions`,
	`(?i)forget\s+(everything|all|your\s+instructions?)`,
	`(?i)new\s+instructions?\s*:`,
	`(?i)you\s+are\s+now\s+(a\s+)?\w+`,
	`(?i)act\s+as\s+(if\s+you\s+(are|were)\s+)?(a\s+)?\w+`,
	// Structural injection (trying to inject fake system/user turns)
	`(?i)(system|assistant|user)\s*:\s*`,
	`(?i)###\s*(system|user|assistant)`,
	`(?i)\[INST\]|\[/INST\]|\[SYS\]`,
	`(?i)<\|im_start\|>|<\|im_end\|>`,
	// Jailbreak keywords
	`(?i)\bDAN\b.*mode`,
	`(?i)do\s+anything\s+now`,
	`(?i)jailbreak`,
	`(?i)prompt\s*injection`,
	// Prompt exfiltration (trying to extract the system prompt)
	`(?i)(print|reveal|show|repeat|output)\s+(your\s+)?(system\s+)?prompt`,
	`(?i)what\s+(are\s+)?your\s+(system\s+)?instructions?`,
}

// piiRules match sensitive patterns that should be masked before LLM calls.
var rawPIIRules = []piiRule{
	{
		re:          regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
		placeholder: "[EMAIL]",
	},
	{
		// E.164 and common US phone formats
		re:          regexp.MustCompile(`\b(\+\d{1,3}[\s\-]?)?\(?\d{3}\)?[\s\-]?\d{3}[\s\-]?\d{4}\b`),
		placeholder: "[PHONE]",
	},
	{
		// US Social Security Number
		re:          regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		placeholder: "[SSN]",
	},
	{
		// Visa / Mastercard / Amex (simplified Luhn pattern)
		re:          regexp.MustCompile(`\b(?:4[0-9]{12}(?:[0-9]{3})?|5[1-5][0-9]{14}|3[47][0-9]{13})\b`),
		placeholder: "[CARD]",
	},
}

// NewGuard creates a Guard with compiled patterns.
func NewGuard() *Guard {
	compiled := make([]*regexp.Regexp, len(rawInjectionPatterns))
	for i, p := range rawInjectionPatterns {
		compiled[i] = regexp.MustCompile(p)
	}
	return &Guard{
		injectionPatterns: compiled,
		piiRules:          rawPIIRules,
	}
}

// CheckInjection returns an error if the query matches any injection pattern.
// This is the fast path — pure regex, no network calls.
func (g *Guard) CheckInjection(query string) error {
	for _, re := range g.injectionPatterns {
		if re.MatchString(query) {
			// Don't tell the attacker which pattern matched — that's a hint.
			return fmt.Errorf("query contains disallowed patterns and was blocked")
		}
	}
	return nil
}

// MaskPII replaces sensitive data with numbered placeholders.
// Example: "Send email to alice@example.com" → "Send email to [EMAIL]_1"
// The masks map lets Restore put the real values back in the LLM's response.
func (g *Guard) MaskPII(query string) *MaskedInput {
	masks := make(map[string]string)
	counter := 0
	sanitized := query

	for _, rule := range g.piiRules {
		sanitized = rule.re.ReplaceAllStringFunc(sanitized, func(match string) string {
			counter++
			placeholder := fmt.Sprintf("%s_%d", rule.placeholder, counter)
			masks[placeholder] = match
			return placeholder
		})
	}

	return &MaskedInput{Sanitized: sanitized, masks: masks}
}

// Restore replaces placeholders back with real values in the LLM's response.
// If the LLM copies a placeholder into its answer (e.g. "I sent an email to [EMAIL]_1"),
// this restores it to "I sent an email to alice@example.com".
func (m *MaskedInput) Restore(response string) string {
	for placeholder, real := range m.masks {
		response = strings.ReplaceAll(response, placeholder, real)
	}
	return response
}

// HasMaskedPII returns true if any PII was found and masked.
func (m *MaskedInput) HasMaskedPII() bool {
	return len(m.masks) > 0
}
