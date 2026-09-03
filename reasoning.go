package plugin

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Reasoning normalization: commandcode's chat/completions returns thinking
// text under message/delta "reasoning" (string) and "reasoning_details"
// (array of {text,...}) but never the standard "reasoning_content" field.
// The host's openai->claude translator only reads reasoning_content, so we
// copy the text over before handing the payload back to the host.
//
// Priority: reasoning_details[].text (authoritative per-token text) first,
// then plain "reasoning". When both carry the same token (observed: details
// mirror the string), details-only avoids duplicating content.

// mapReasoningBody walks choices[].delta/message and backfills
// reasoning_content. Returns the rewritten body and whether it changed.
func mapReasoningBody(body []byte) ([]byte, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false
	}
	if !strings.Contains(string(body), "reasoning") {
		return body, false
	}
	choices := gjson.GetBytes(body, "choices")
	if !choices.IsArray() {
		return body, false
	}
	out := body
	changed := false
	choiceIdx := -1
	choices.ForEach(func(_, choice gjson.Result) bool {
		choiceIdx++
		for _, field := range []string{"delta", "message"} {
			msg := choice.Get(field)
			if !msg.Exists() || !msg.IsObject() {
				continue
			}
			text, ok := reasoningText(msg)
			if !ok || text == "" {
				continue
			}
			if existing := msg.Get("reasoning_content"); existing.Exists() {
				if strings.TrimSpace(existing.String()) != "" {
					continue
				}
			}
			updated, err := sjson.SetBytes(out, "choices."+itoa(int64(choiceIdx))+"."+field+".reasoning_content", text)
			if err != nil {
				continue
			}
			out = updated
			changed = true
		}
		return true
	})
	return out, changed
}

// reasoningText extracts thinking text from one delta/message object.
func reasoningText(msg gjson.Result) (string, bool) {
	if details := msg.Get("reasoning_details"); details.IsArray() {
		var parts []string
		for _, d := range details.Array() {
			if t := d.Get("text").String(); strings.TrimSpace(t) != "" {
				parts = append(parts, t)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ""), true
		}
	}
	if r := msg.Get("reasoning"); r.Type == gjson.String {
		if t := strings.TrimSpace(r.String()); t != "" {
			return r.String(), true
		}
	}
	return "", false
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
