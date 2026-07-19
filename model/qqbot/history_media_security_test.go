package qqbot

import (
	"strings"
	"testing"
)

func TestSafeHistoryPartRemovesPathSyntax(t *testing.T) {
	t.Parallel()

	for _, value := range []string{".", "..", "../other", `..\\other`, "message.id", "群/成员"} {
		got := safeHistoryPart(value)
		if got == "." || got == ".." || strings.ContainsAny(got, `./\\`) {
			t.Fatalf("safeHistoryPart(%q) = %q, contains path syntax", value, got)
		}
	}
}
