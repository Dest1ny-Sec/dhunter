package toolbelt

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSafeExecMissingBinaryReturnsFriendlyMessage(t *testing.T) {
	out, err := safeExec(context.Background(), 5*time.Second, "dhunter_definitely_not_installed_bin_xyz", "-h")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "未安装") || !strings.Contains(err.Error(), "替代") {
		t.Fatalf("expected graceful-degradation message, got: %v", err)
	}
	_ = out
}
