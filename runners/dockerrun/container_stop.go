package dockerrun

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/codefly-dev/core/internal/wire"
	"github.com/docker/docker/api/types/container"
	"gopkg.in/yaml.v3"
)

// MaxContainerStopExpectationBytes includes the default JSON Encoder newline.
// Oversized inspections are refused rather than emitting an unusable receipt.
const MaxContainerStopExpectationBytes = 64 << 10

// ContainerStopExpectation is an untrusted observation of an explicitly
// selected container. It grants no authority and establishes no parent service.
// The caller owns operator authorization; Core compares every field before stop.
type ContainerStopExpectation struct {
	ID      string                 `json:"id"`
	Created string                 `json:"created"`
	Name    string                 `json:"name"`
	Labels  map[string]string      `json:"labels"`
	Mounts  []container.MountPoint `json:"mounts"`
}

// ContainerStopResult describes observed postconditions, including on error.
// Success requires nil error and all three true. This is not data-content proof.
type ContainerStopResult struct {
	Stopped         bool `json:"stopped"`
	Retained        bool `json:"retained"`
	MountsUnchanged bool `json:"mounts_unchanged"`
}

func validateFullContainerID(id string) error {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 || id != strings.ToLower(id) {
		return errors.New("container stop requires a full lowercase 64-hex Docker ID")
	}
	return nil
}

func (expected ContainerStopExpectation) validate() error {
	if err := validateFullContainerID(expected.ID); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, expected.Created); err != nil {
		return fmt.Errorf("invalid container creation evidence: %w", err)
	}
	if expected.Name == "" || expected.Labels[LabelCodeflyOwner] != "true" ||
		expected.Labels[LabelCodeflyName] != expected.Name || expected.Mounts == nil {
		return errors.New("container stop requires exact Codefly name, owner, labels and explicit mount evidence")
	}
	data, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	if len(data)+1 > MaxContainerStopExpectationBytes {
		return errors.New("container stop expectation is too large")
	}
	return nil
}

// ParseContainerStopExpectation decodes a complete saved preview. Duplicate
// keys, nulls, unknown fields, trailing documents and missing evidence refuse.
func ParseContainerStopExpectation(data []byte) (ContainerStopExpectation, error) {
	var expected ContainerStopExpectation
	if len(data) > MaxContainerStopExpectationBytes {
		return expected, errors.New("container stop expectation is too large")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return expected, err
	}
	if defect := wire.Check(&document); defect.Kind != wire.NoDefect {
		return expected, fmt.Errorf("invalid container evidence at %s: %v (%s)", defect.Path, defect.Kind, defect.Detail)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expected); err != nil {
		return expected, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return expected, errors.New("container stop expectation has trailing data")
	}
	if err := checkContainerStopFields(document.Content[0]); err != nil {
		return expected, err
	}
	return expected, expected.validate()
}

// encoding/json accepts case-insensitive struct fields. Check the two schema
// objects with the existing wire mapping reader before admitting that binding;
// arbitrary label keys remain case-sensitive data, not schema field names.
func checkContainerStopFields(node *yaml.Node) error {
	mapping := wire.ReadMapping(node, func(key string, value *yaml.Node) (bool, error) {
		switch key {
		case "id", "created", "name", "labels":
			return true, nil
		case "mounts":
			for _, item := range value.Content {
				present := make(map[string]bool)
				fields := wire.ReadMapping(item, func(key string, _ *yaml.Node) (bool, error) {
					switch key {
					case "Type", "Name", "Source", "Destination", "Driver", "Mode", "RW", "Propagation":
						present[key] = true
						return true, nil
					default:
						return false, nil
					}
				})
				if fields.Err != nil {
					return true, fields.Err
				}
				if fields.HasUnknown {
					return true, fmt.Errorf("unknown container mount field %q", fields.Unknown)
				}
				// These MountPoint fields have no omitempty tag. Their zero
				// values must be supplied explicitly, not invented by decoding.
				for _, field := range []string{"Source", "Destination", "Mode", "RW", "Propagation"} {
					if !present[field] {
						return true, fmt.Errorf("missing container mount field %q", field)
					}
				}
			}
			return true, nil
		default:
			return false, nil
		}
	})
	if mapping.Err != nil {
		return mapping.Err
	}
	if mapping.HasUnknown {
		return fmt.Errorf("unknown container stop field %q", mapping.Unknown)
	}
	return nil
}

func canonicalContainerMounts(mounts []container.MountPoint) []container.MountPoint {
	result := append([]container.MountPoint{}, mounts...)
	slices.SortFunc(result, func(a, b container.MountPoint) int {
		// MountPoint contains only strings and a bool: Marshal cannot fail.
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return bytes.Compare(left, right)
	})
	return result
}

func containerStopObservation(inspect container.InspectResponse, id string) (ContainerStopExpectation, error) {
	if inspect.ContainerJSONBase == nil || inspect.ID != id || inspect.Config == nil ||
		inspect.HostConfig == nil || inspect.State == nil {
		return ContainerStopExpectation{}, errors.New("container inspection has incomplete or changed identity")
	}
	if inspect.HostConfig.AutoRemove {
		return ContainerStopExpectation{}, errors.New("container stop refuses AutoRemove: exit could delete the retained object")
	}
	expected := ContainerStopExpectation{
		ID: inspect.ID, Created: inspect.Created, Name: strings.TrimPrefix(inspect.Name, "/"),
		Labels: maps.Clone(inspect.Config.Labels), Mounts: canonicalContainerMounts(inspect.Mounts),
	}
	return expected, expected.validate()
}

// PreviewContainerStop inspects one full immutable ID without any lifecycle
// effect. AutoRemove objects refuse; stopping them could delete their data.
func PreviewContainerStop(ctx context.Context, id string) (*ContainerStopExpectation, error) {
	if err := validateFullContainerID(id); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cli, err := NewClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	inspect, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	expected, err := containerStopObservation(inspect, id)
	if err != nil {
		return nil, err
	}
	return &expected, nil
}

// StopContainer conditionally stops one operator-selected immutable ID. It does
// not resolve a name, claim a ledger resource, relabel, remove or restart it.
// A canceled or failed call may have stopped the container; only readback can
// establish the postconditions. Another actor can restart it after observation.
func StopContainer(ctx context.Context, expected ContainerStopExpectation) (*ContainerStopResult, error) {
	result := &ContainerStopResult{}
	if err := expected.validate(); err != nil {
		return result, err
	}
	expected.Mounts = canonicalContainerMounts(expected.Mounts)
	cli, err := NewClient()
	if err != nil {
		return result, err
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	before, err := cli.ContainerInspect(ctx, expected.ID)
	if err != nil {
		return result, err
	}
	observed, err := containerStopObservation(before, expected.ID)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(expected, observed) {
		return result, errors.New("container stop evidence changed")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var stopErr error
	if before.State.Running {
		grace := 3
		stopErr = cli.ContainerStop(ctx, expected.ID, container.StopOptions{Timeout: &grace})
	}
	after, err := cli.ContainerInspect(ctx, expected.ID)
	if err != nil {
		return result, errors.Join(stopErr, fmt.Errorf("cannot verify retained container: %w", err))
	}
	if after.ContainerJSONBase == nil || after.ID != expected.ID || after.Created != expected.Created {
		return result, errors.Join(stopErr, errors.New("retained container identity changed"))
	}
	result.Retained = true
	result.Stopped = after.State != nil && !after.State.Running && !after.State.Restarting && !after.State.Paused
	result.MountsUnchanged = reflect.DeepEqual(expected.Mounts, canonicalContainerMounts(after.Mounts))
	observed, inspectErr := containerStopObservation(after, expected.ID)
	if inspectErr != nil || !reflect.DeepEqual(expected, observed) || !result.Stopped {
		return result, errors.Join(stopErr, inspectErr, errors.New("container stop postconditions were not established"))
	}
	return result, stopErr
}
