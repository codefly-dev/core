package docker

import "testing"

func TestIsValidDockerImageName(t *testing.T) {
	tests := []struct {
		imageName string
		valid     bool
	}{
		{"examples/counter-go-grpc-nextjs-postgres/backend", true},
	}
	for _, tt := range tests {
		t.Run(tt.imageName, func(t *testing.T) {
			if got := IsValidDockerImageName(tt.imageName); got != tt.valid {
				t.Errorf("IsValidDockerImageName() = %v, want %v", got, tt.valid)
			}
		})
	}
}
