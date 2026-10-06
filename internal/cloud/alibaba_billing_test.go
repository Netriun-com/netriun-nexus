// SPDX-License-Identifier: AGPL-3.0-only

package cloud

import "testing"

func TestClassifyAlibabaBillService(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "ecs code", values: []string{"ecs", "Elastic Compute Service", "instance"}, want: "ecs"},
		{name: "ecs display name", values: []string{"unknown", "Elastic Compute Service", ""}, want: "ecs"},
		{name: "eds code", values: []string{"ecd", "WUYING Workspace", "Cloud Desktop"}, want: "eds"},
		{name: "eds display name", values: []string{"unknown", "Elastic Desktop Service", ""}, want: "eds"},
		{name: "other", values: []string{"oss", "Object Storage Service", "bucket"}, want: "other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyAlibabaBillService(test.values...); got != test.want {
				t.Fatalf("classifyAlibabaBillService(%q) = %q, want %q", test.values, got, test.want)
			}
		})
	}
}
