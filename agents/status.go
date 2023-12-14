package agents

import servicev1 "github.com/codefly-dev/core/generated/v1/go/proto/services"

const (
	InitError   = servicev1.InitStatus_ERROR
	InitSuccess = servicev1.InitStatus_READY
)
