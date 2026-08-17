package resources

import "testing"

func TestServiceAutoscaleValidate(t *testing.T) {
	if err := (*ServiceAutoscale)(nil).Validate(); err != nil {
		t.Fatalf("nil autoscale is a valid not-declared state: %v", err)
	}
	valid := &ServiceAutoscale{Min: 2, Max: 5, TargetCPU: 70}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid autoscale rejected: %v", err)
	}
	tests := map[string]*ServiceAutoscale{
		"min below one":   {Min: 0, Max: 5, TargetCPU: 70},
		"max below min":   {Min: 3, Max: 2, TargetCPU: 70},
		"target cpu zero": {Min: 1, Max: 2, TargetCPU: 0},
		"target cpu over": {Min: 1, Max: 2, TargetCPU: 101},
	}
	for name, autoscale := range tests {
		t.Run(name, func(t *testing.T) {
			if err := autoscale.Validate(); err == nil {
				t.Fatalf("expected %s to fail validation", name)
			}
		})
	}
}

func TestEnvironmentResourceQuotaValidate(t *testing.T) {
	if err := (*EnvironmentResourceQuota)(nil).Validate(); err != nil {
		t.Fatalf("nil quota is a valid not-declared state: %v", err)
	}
	if err := (&EnvironmentResourceQuota{}).Validate(); err == nil {
		t.Fatal("expected an empty quota to fail validation")
	}
	if err := (&EnvironmentResourceQuota{
		DefaultContainer: &EnvironmentContainerResources{},
	}).Validate(); err == nil {
		t.Fatal("expected a quota whose only block is an empty default-container to fail validation")
	}
	valid := []*EnvironmentResourceQuota{
		{Requests: &EnvironmentResourceList{CPU: "4"}},
		{Limits: &EnvironmentResourceList{Memory: "16Gi"}},
		{Pods: "50"},
		{DefaultContainer: &EnvironmentContainerResources{Requests: &EnvironmentResourceList{CPU: "100m"}}},
	}
	for _, quota := range valid {
		if err := quota.Validate(); err != nil {
			t.Fatalf("valid quota %+v rejected: %v", quota, err)
		}
	}
}
