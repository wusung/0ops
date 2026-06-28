package gitops

import "strings"

// ActionGitopsUnauthorizedCommit is the audit_log action emitted when a gitops
// commit fails backend-authorship classification (supply-chain-security spec
// § 7.2, ADR-0017 § 3.5). It is a detective control complementing the
// preventive branch-protection rules; v1 detects + alerts only (no
// auto-revert — spec § 10 / open question).
const ActionGitopsUnauthorizedCommit = "gitops_unauthorized_commit"

// CommitMeta carries the verifiable attributes of a single gitops commit used
// to decide whether it was an authorized backend write.
type CommitMeta struct {
	// AuthorEmail is the commit author email (git `%ae`).
	AuthorEmail string
	// SignerIdentity is the principal of the verified signature (the SSH
	// allowed-signer identity). Empty when the commit is unsigned or the
	// signature did not verify.
	SignerIdentity string
	// GoodSignature reports whether the signature verified against an
	// allowed signer.
	GoodSignature bool
	// Message is the full commit message; its first line carries the
	// machine-parseable `<action>: <team>/<app> @ <deploy_run_id>` contract.
	Message string
}

// ExpectedSigner pins the only legitimate gitops writer identity (the ops-bot
// author email + its allowed-signer principal).
type ExpectedSigner struct {
	AuthorEmail    string
	SignerIdentity string
}

// ClassifyCommit reports whether a gitops commit is an authorized backend
// write. A commit is authorized iff all of the following hold:
//
//	(a) it carries a good signature,
//	(b) the signer principal matches the expected ops-bot signer,
//	(c) the author email matches ops-bot, and
//	(d) the commit message carries a deploy_run_id per the commit contract.
//
// Any failure returns (false, reason) where reason is a stable machine token
// suitable for the audit_log Result. Pure; performs no I/O. Wiring this into
// the reconciler / push-webbook loop is deferred (spec § 7.2 v1 = detection
// pure function; ADR-0017 open question on auto-revert).
func ClassifyCommit(meta CommitMeta, expected ExpectedSigner) (authorized bool, reason string) {
	if !meta.GoodSignature {
		return false, "unsigned_or_bad_signature"
	}
	signer := strings.TrimSpace(meta.SignerIdentity)
	if signer == "" || signer != strings.TrimSpace(expected.SignerIdentity) {
		return false, "signer_not_ops_bot"
	}
	if !strings.EqualFold(strings.TrimSpace(meta.AuthorEmail), strings.TrimSpace(expected.AuthorEmail)) {
		return false, "author_not_ops_bot"
	}
	if _, ok := parseDeployRunID(meta.Message); !ok {
		return false, "missing_deploy_run_id"
	}
	return true, ""
}

// parseDeployRunID extracts the deploy_run_id from a commit message whose first
// line follows the `<action>: <team>/<app> @ <deploy_run_id>` contract (see
// CommitMessage.String). Returns ("", false) when the contract is absent.
func parseDeployRunID(message string) (string, bool) {
	line := message
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	idx := strings.LastIndex(line, " @ ")
	if idx < 0 {
		return "", false
	}
	id := strings.TrimSpace(line[idx+len(" @ "):])
	if id == "" {
		return "", false
	}
	return id, true
}
