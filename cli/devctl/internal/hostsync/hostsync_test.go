package hostsync

import (
	"strings"
	"testing"
)

func TestCollectIngressHosts(t *testing.T) {
	got := CollectIngressHosts([]string{" Ouroboros.test ", "webserver.ouroboros.test", "ouroboros.test"})
	want := []string{"ouroboros.test", "webserver.ouroboros.test"}
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestUpsertManagedBlockIdempotent(t *testing.T) {
	base := "127.0.0.1 localhost\n"
	first, err := UpsertManagedBlock(base, "dev-all", "127.0.0.1", []string{"ouroboros.test", "webserver.ouroboros.test"})
	if err != nil {
		t.Fatalf("first upsert error: %v", err)
	}
	second, err := UpsertManagedBlock(first, "dev-all", "127.0.0.1", []string{"ouroboros.test", "webserver.ouroboros.test"})
	if err != nil {
		t.Fatalf("second upsert error: %v", err)
	}
	if first != second {
		t.Fatalf("upsert not idempotent\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMissingMappings(t *testing.T) {
	content := strings.Join([]string{
		"127.0.0.1 localhost",
		"127.0.0.1 ouroboros.test",
		"172.30.10.5 webserver.ouroboros.test",
	}, "\n")
	missing := MissingMappings(content, "127.0.0.1", []string{"ouroboros.test", "webserver.ouroboros.test"})
	if len(missing) != 1 || missing[0] != "webserver.ouroboros.test" {
		t.Fatalf("unexpected missing=%v", missing)
	}
}
