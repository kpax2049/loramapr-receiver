package diagnostics

import "testing"

func TestDeriveAttention(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    Finding
		ops   OperationalSummary
		state AttentionState
		cat   AttentionCategory
	}{
		{
			name: "none when healthy",
			in:   Finding{},
			ops: OperationalSummary{
				Overall: "ok",
				Summary: "healthy",
			},
			state: AttentionNone,
			cat:   AttentionCategoryNone,
		},
		{
			name: "degraded info without finding",
			in:   Finding{},
			ops: OperationalSummary{
				Overall: "degraded",
				Summary: "receiver is degraded",
			},
			state: AttentionInfo,
			cat:   AttentionCategoryService,
		},
		{
			name: "lifecycle urgent",
			in: Finding{
				Code:    FailureReceiverRevoked,
				Summary: "revoked",
				Hint:    "re-pair",
			},
			state: AttentionUrgent,
			cat:   AttentionCategoryLifecycle,
		},
		{
			name: "outdated action required",
			in: Finding{
				Code:    FailureReceiverOutdated,
				Summary: "outdated",
			},
			state: AttentionActionRequired,
			cat:   AttentionCategoryVersion,
		},
		{
			name: "compat urgent",
			in: Finding{
				Code:    FailureLocalSchemaIncompat,
				Summary: "incompatible",
			},
			state: AttentionUrgent,
			cat:   AttentionCategoryCompatibility,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DeriveAttention(tc.in, tc.ops)
			if got.State != tc.state {
				t.Fatalf("expected state %q, got %q", tc.state, got.State)
			}
			if got.Category != tc.cat {
				t.Fatalf("expected category %q, got %q", tc.cat, got.Category)
			}
		})
	}
}
