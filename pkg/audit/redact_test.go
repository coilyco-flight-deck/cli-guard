package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func samplePolicy() RedactPolicy {
	return RedactPolicy{
		SecretFlagPatterns: []string{
			"--secret", "--password", "--passwd", "--token", "--api-key",
		},
		IdentifierPatterns: []*regexp.Regexp{
			regexp.MustCompile(`\b\d{12}\b`),
			regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`),
		},
	}
}

func TestRedactArgv_LowIsPassthrough(t *testing.T) {
	in := []string{"--password=hunter2", "rest"}
	got := RedactArgv(in, DataSecurityLow, samplePolicy())
	if &got[0] != &in[0] && got[0] != in[0] {
		t.Errorf("low should be passthrough, got %v", got)
	}
}

func TestRedactArgv_MediumEqualsForm(t *testing.T) {
	in := []string{"ssm", "--password=hunter2", "--quiet"}
	got := RedactArgv(in, DataSecurityMedium, samplePolicy())
	if got[1] != "--password=[REDACTED]" {
		t.Errorf("got[1] = %q, want --password=[REDACTED]", got[1])
	}
	if got[2] != "--quiet" {
		t.Errorf("unrelated token mutated: %q", got[2])
	}
}

func TestRedactArgv_MediumBareForm(t *testing.T) {
	in := []string{"ssm", "--token", "abcd1234", "--region", "us-east-1"}
	got := RedactArgv(in, DataSecurityMedium, samplePolicy())
	if got[2] != RedactedValue {
		t.Errorf("token value not redacted: %v", got)
	}
	if got[3] != "--region" || got[4] != "us-east-1" {
		t.Errorf("unrelated flag mutated: %v", got)
	}
}

func TestRedactArgv_MaxDropsArgvWhenMatched(t *testing.T) {
	in := []string{"ssm", "--password=hunter2"}
	got := RedactArgv(in, DataSecurityMax, samplePolicy())
	if got != nil {
		t.Errorf("max should return nil argv when matched, got %v", got)
	}
}

func TestRedactArgv_MaxKeepsArgvWhenNoMatch(t *testing.T) {
	in := []string{"ssm", "--region", "us-east-1"}
	got := RedactArgv(in, DataSecurityMax, samplePolicy())
	if len(got) != 3 {
		t.Errorf("max with no match should preserve argv, got %v", got)
	}
}

func TestRedactIdentifiersInString_HighRedactsAccountAndEmail(t *testing.T) {
	in := "AccessDenied for account 123456789012 user user@example.com"
	got := RedactIdentifiersInString(in, DataSecurityHigh, samplePolicy())
	if strings.Contains(got, "123456789012") {
		t.Errorf("account id not redacted: %q", got)
	}
	if strings.Contains(got, "user@example.com") {
		t.Errorf("email not redacted: %q", got)
	}
}

func TestRedactEgressRows_HighStripsSubdomain(t *testing.T) {
	in := []EgressRow{{Host: "api.github.com", BytesDown: 100}}
	got := RedactEgressRows(in, DataSecurityHigh)
	if got[0].Host != "github.com" {
		t.Errorf("host = %q, want github.com", got[0].Host)
	}
	if got[0].BytesDown != 100 {
		t.Errorf("byte counts must survive redaction")
	}
}

func TestRedactEgressRows_MaxRedactsHost(t *testing.T) {
	in := []EgressRow{{Host: "api.github.com", BytesDown: 100, DurationMS: 50}}
	got := RedactEgressRows(in, DataSecurityMax)
	if got[0].Host != RedactedValue {
		t.Errorf("host = %q, want [REDACTED]", got[0].Host)
	}
	if got[0].DurationMS != 0 {
		t.Errorf("duration must be zeroed at max")
	}
	if got[0].BytesDown != 100 {
		t.Errorf("byte counts must survive")
	}
}

func TestWriter_Append_BackwardsCompatNoProfileDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w := NewWriter(path)
	w.SetRedactPolicy(samplePolicy())
	defer func() { _ = w.Close() }()
	rec := Record{
		Verb:     "ops.aws.ssm",
		Decision: DecisionAccept,
		Argv:     []string{"ops", "aws", "ssm", "--password=hunter2"},
	}
	if err := w.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Record
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v line=%s", err, b)
	}
	if got.Argv[3] != "--password=hunter2" {
		t.Errorf("ProfileDecision=nil should bypass redaction, got %v", got.Argv)
	}
}

func TestWriter_Append_RedactsWhenProfileAware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w := NewWriter(path)
	w.SetRedactPolicy(samplePolicy())
	defer func() { _ = w.Close() }()
	rec := Record{
		Verb:     "ops.aws.ssm",
		Decision: DecisionAccept,
		Argv:     []string{"ops", "aws", "ssm", "--password=hunter2"},
		ProfileDecision: &ProfileDecision{
			Allowed:    true,
			Coordinate: Coordinate{DataSecurity: DataSecurityHigh},
		},
	}
	if err := w.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Record
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v line=%s", err, b)
	}
	if got.Argv[3] != "--password=[REDACTED]" {
		t.Errorf("argv not redacted at high: %v", got.Argv)
	}
}

func TestWriter_Append_MaxDropsArgv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w := NewWriter(path)
	w.SetRedactPolicy(samplePolicy())
	defer func() { _ = w.Close() }()
	rec := Record{
		Verb:     "ops.aws.ssm",
		Decision: DecisionAccept,
		Argv:     []string{"ops", "aws", "ssm", "--password=hunter2"},
		ProfileDecision: &ProfileDecision{
			Allowed:    true,
			Coordinate: Coordinate{DataSecurity: DataSecurityMax},
		},
	}
	if err := w.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Record
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v line=%s", err, b)
	}
	if len(got.Argv) != 0 {
		t.Errorf("max should drop argv when matched, got %v", got.Argv)
	}
	if got.Verb != "ops.aws.ssm" {
		t.Errorf("verb dropped at max: %q", got.Verb)
	}
}
