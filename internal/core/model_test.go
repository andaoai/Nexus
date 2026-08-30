package core

import (
	"strings"
	"testing"
)

func TestNewID(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := NewID("c")
		if !strings.HasPrefix(id, "c-") || len(id) != len("c-")+6 {
			t.Fatalf("格式错误: %q", id)
		}
		if ids[id] {
			t.Fatalf("ID 重复: %q", id)
		}
		ids[id] = true
	}
}
