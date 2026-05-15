package audit

import "strings"

// redactedToken is the replacement value substituted for any field that
// matches the redaction rule set. The string is stable so that tests
// and downstream tooling can assert on it directly.
const redactedToken = "***"

// sensitiveKeySubstrings collects the case-insensitive substrings that
// trigger redaction (spec § 8.1, error-model § 9.1). Match is by
// substring so prefixed and suffixed forms (e.g. `secret_pat`, `api_secret`,
// `gha_token`, `_signature`) all hit the same rule.
var sensitiveKeySubstrings = []string{
	"secret",
	"password",
	"token",
	"signature",
	"private_key",
	"authorization",
	"cookie",
	"bearer",
}

// Redact returns a deep copy of value with every sensitive field
// replaced by redactedToken. Maps and slices are walked recursively;
// other concrete types are returned untouched. Callers MUST run this
// against any payload that ends up in audit_log.args / audit_log.result
// (spec § 15 hard rule #2).
func Redact(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			if isSensitiveKey(k) {
				out[k] = redactedToken
				continue
			}
			out[k] = Redact(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = Redact(child)
		}
		return out
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	lowered := strings.ToLower(key)
	for _, needle := range sensitiveKeySubstrings {
		if strings.Contains(lowered, needle) {
			return true
		}
	}
	return false
}
