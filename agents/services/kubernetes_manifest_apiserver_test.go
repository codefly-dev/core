package services

import (
	"fmt"
	"strings"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// apiServerManifest renders a conformant Deployment whose pod either declares
// API-server access or does not, and either projects a token or does not.
func apiServerManifest(annotation string, automount bool) []byte {
	annotations := ""
	if annotation != "" {
		annotations = fmt.Sprintf("\n      annotations:\n        %s: %q", AnnotationAPIServerAccess, annotation)
	}
	return []byte(fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: widget
  namespace: demo
  labels:
    app.kubernetes.io/managed-by: codefly
spec:
  selector:
    matchLabels:
      app: widget
  template:
    metadata:
      labels:
        app: widget%s
    spec:
      automountServiceAccountToken: %t
      terminationGracePeriodSeconds: 30
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: service
          image: registry.example.com/widget@sha256:%s
          securityContext:
            allowPrivilegeEscalation: false
            runAsNonRoot: true
            readOnlyRootFilesystem: true
            seccompProfile:
              type: RuntimeDefault
            capabilities:
              drop: [ALL]
          resources:
            requests:
              cpu: 10m
              memory: 16Mi
            limits:
              cpu: 100m
              memory: 64Mi
          startupProbe:
            exec:
              command: ["true"]
          readinessProbe:
            exec:
              command: ["true"]
          livenessProbe:
            exec:
              command: ["true"]
`, annotations, automount, strings.Repeat("a", 64)))
}

func automountViolations(t *testing.T, annotation string, automount bool) []string {
	t.Helper()
	var kept []string
	for _, violation := range validateKubernetesManifest(apiServerManifest(annotation, automount), "demo", builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1) {
		if strings.Contains(violation, "automountServiceAccountToken") || strings.Contains(violation, AnnotationAPIServerAccess) {
			kept = append(kept, violation)
		}
	}
	return kept
}

// A workload that says nothing is refused exactly as it was before the opt-in
// existed: the default is still that no token is projected.
func TestUndeclaredWorkloadStillMayNotProjectAServiceAccountToken(t *testing.T) {
	if got := automountViolations(t, "", true); len(got) == 0 {
		t.Fatal("projecting a token without declaring API-server access was accepted")
	}
	if got := automountViolations(t, "", false); len(got) != 0 {
		t.Fatalf("the conformant default was rejected: %v", got)
	}
}

// The knob service-python-fastapi#44 shipped has to be turnable, or it is a
// constant dressed as a parameter.
func TestDeclaredAPIServerAccessPermitsTheProjection(t *testing.T) {
	if got := automountViolations(t, APIServerAccessRequired, true); len(got) != 0 {
		t.Fatalf("a workload that declared API-server access was still refused: %v", got)
	}
}

// A declaration that changes nothing is one nobody revisits, so it is refused
// rather than carried as decoration.
func TestDeclaredAPIServerAccessWithoutProjectionIsRefused(t *testing.T) {
	if got := automountViolations(t, APIServerAccessRequired, false); len(got) == 0 {
		t.Fatal("a declaration that projects nothing was accepted")
	}
}

// Fail closed: an unrecognised value is not quietly read as permission, and not
// quietly read as refusal either.
func TestUnrecognisedAPIServerAccessDeclarationIsRefused(t *testing.T) {
	for _, value := range []string{"true", "yes", "Required", "optional"} {
		t.Run(value, func(t *testing.T) {
			if got := automountViolations(t, value, true); len(got) == 0 {
				t.Fatalf("declaration %q was accepted as permission", value)
			}
		})
	}
}
