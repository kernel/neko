package session

import (
	"testing"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/pkg/types"
)

func TestLegacyIsHost(t *testing.T) {
	tests := []struct {
		name            string
		implicitHosting bool
		canHost         bool
		setHost         bool
		want            bool
	}{
		{name: "implicit hosting, can host", implicitHosting: true, canHost: true, want: true},
		{name: "implicit hosting, cannot host", implicitHosting: true, canHost: false, want: false},
		{name: "explicit hosting, host", canHost: true, setHost: true, want: true},
		{name: "explicit hosting, not host", canHost: true, want: false},
		{name: "explicit hosting, cannot host", canHost: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := New(&config.Session{ImplicitHosting: tt.implicitHosting})
			session, _, err := manager.Create("member", types.MemberProfile{CanHost: tt.canHost})
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			if tt.setHost {
				session.SetAsHost()
			}

			if got := session.LegacyIsHost(); got != tt.want {
				t.Errorf("LegacyIsHost() = %v, want %v", got, tt.want)
			}
		})
	}
}
