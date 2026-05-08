package actionsgithubcom

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeNode(labels map[string]string, ready bool, unschedulable bool) corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: status},
			},
		},
	}
}

func TestNodeIsEligible(t *testing.T) {
	now := metav1.Now()
	tests := []struct {
		name          string
		ready         bool
		unschedulable bool
		deleting      bool
		want          bool
	}{
		{"ready and schedulable", true, false, false, true},
		// NotReady nodes count: they are being initialised by CAS and represent
		// capacity the cloud provider has already allocated.
		{"not ready but schedulable", false, false, false, true},
		{"ready but cordoned", true, true, false, false},
		{"not ready and cordoned", false, true, false, false},
		{"being deleted", true, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := makeNode(nil, tt.ready, tt.unschedulable)
			if tt.deleting {
				node.DeletionTimestamp = &now
			}
			if got := nodeIsEligible(&node); got != tt.want {
				t.Errorf("nodeIsEligible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeMatchesAffinityTerms(t *testing.T) {
	gpuLabels := map[string]string{
		"cloud.google.com/compute-class": "gpu1",
	}
	otherLabels := map[string]string{
		"cloud.google.com/compute-class": "standard",
	}

	terms := []corev1.NodeSelectorTerm{
		{
			MatchExpressions: []corev1.NodeSelectorRequirement{
				{
					Key:      "cloud.google.com/gke-nodepool",
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"gpu1"},
				},
			},
		},
		{
			MatchExpressions: []corev1.NodeSelectorRequirement{
				{
					Key:      "cloud.google.com/compute-class",
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"gpu1"},
				},
			},
		},
	}

	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"matches second term (compute-class=gpu1)", gpuLabels, true},
		{"no match", otherLabels, false},
		{"matches first term (gke-nodepool=gpu1)", map[string]string{"cloud.google.com/gke-nodepool": "gpu1"}, true},
		{"matches both terms", map[string]string{
			"cloud.google.com/gke-nodepool":   "gpu1",
			"cloud.google.com/compute-class": "gpu1",
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := makeNode(tt.labels, true, false)
			if got := nodeMatchesAffinityTerms(&node, terms); got != tt.want {
				t.Errorf("nodeMatchesAffinityTerms() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeSelectorRequirementMatches(t *testing.T) {
	labels := map[string]string{"env": "prod", "zone": "us-east"}

	tests := []struct {
		name string
		req  corev1.NodeSelectorRequirement
		want bool
	}{
		{
			"In - matches",
			corev1.NodeSelectorRequirement{Key: "env", Operator: corev1.NodeSelectorOpIn, Values: []string{"prod", "staging"}},
			true,
		},
		{
			"In - no match",
			corev1.NodeSelectorRequirement{Key: "env", Operator: corev1.NodeSelectorOpIn, Values: []string{"dev"}},
			false,
		},
		{
			"In - key missing",
			corev1.NodeSelectorRequirement{Key: "missing", Operator: corev1.NodeSelectorOpIn, Values: []string{"prod"}},
			false,
		},
		{
			"NotIn - excluded value",
			corev1.NodeSelectorRequirement{Key: "env", Operator: corev1.NodeSelectorOpNotIn, Values: []string{"prod"}},
			false,
		},
		{
			"NotIn - not excluded",
			corev1.NodeSelectorRequirement{Key: "env", Operator: corev1.NodeSelectorOpNotIn, Values: []string{"dev"}},
			true,
		},
		{
			"NotIn - key missing",
			corev1.NodeSelectorRequirement{Key: "missing", Operator: corev1.NodeSelectorOpNotIn, Values: []string{"prod"}},
			true,
		},
		{
			"Exists - present",
			corev1.NodeSelectorRequirement{Key: "env", Operator: corev1.NodeSelectorOpExists},
			true,
		},
		{
			"Exists - absent",
			corev1.NodeSelectorRequirement{Key: "missing", Operator: corev1.NodeSelectorOpExists},
			false,
		},
		{
			"DoesNotExist - absent",
			corev1.NodeSelectorRequirement{Key: "missing", Operator: corev1.NodeSelectorOpDoesNotExist},
			true,
		},
		{
			"DoesNotExist - present",
			corev1.NodeSelectorRequirement{Key: "env", Operator: corev1.NodeSelectorOpDoesNotExist},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nodeSelectorRequirementMatches(labels, tt.req); got != tt.want {
				t.Errorf("nodeSelectorRequirementMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequiredNodeAffinity(t *testing.T) {
	terms := []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: "foo", Operator: corev1.NodeSelectorOpExists,
		}},
	}}

	tests := []struct {
		name    string
		podSpec *corev1.PodSpec
		wantNil bool
	}{
		{"nil affinity", &corev1.PodSpec{}, true},
		{"nil node affinity", &corev1.PodSpec{Affinity: &corev1.Affinity{}}, true},
		{"nil required", &corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{}}}, true},
		{"has required terms", &corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: terms},
		}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := requiredNodeAffinity(tt.podSpec)
			if (got == nil) != tt.wantNil {
				t.Errorf("requiredNodeAffinity() nil=%v, want nil=%v", got == nil, tt.wantNil)
			}
		})
	}
}
