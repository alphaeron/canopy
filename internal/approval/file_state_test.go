package approval

import "testing"

func TestFileStateOwnership(t *testing.T) {
	tests := []struct {
		name string
		fs   FileState
		want Ownership
	}{
		{"satisfied by rules", FileState{Status: StatusSatisfied}, OwnedByRules},
		{"awaiting rule approvers", FileState{Status: StatusMissingApprovers}, OwnedByRules},
		{"repository owners", FileState{Status: StatusMissingApprovers, UsedFallback: true}, OwnedByFallback},
		{"no active repository owners", FileState{Status: StatusError, UsedFallback: true}, OwnedByFallback},
		{"unresolved target", FileState{Status: StatusUnresolved, UsedFallback: true}, OwnershipBroken},
	}
	for _, tc := range tests {
		if got := tc.fs.Ownership(); got != tc.want {
			t.Errorf("%s: Ownership = %v, want %v", tc.name, got, tc.want)
		}
	}
}
