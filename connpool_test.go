package mpx

import (
	"testing"
)

func TestNewConnPool(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "创建ConnPool",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewConnPool(); got == nil {
				t.Errorf("NewConnPool() = nil")
			}
		})
	}
}
