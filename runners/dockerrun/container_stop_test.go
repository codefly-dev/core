package dockerrun

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/require"
)

func stopExpectationFixture() ContainerStopExpectation {
	return ContainerStopExpectation{
		ID: strings.Repeat("a", 64), Created: "2026-10-28T00:38:43.921733968Z", Name: "codefly-test",
		Labels: map[string]string{LabelCodeflyOwner: "true", LabelCodeflyName: "codefly-test"},
		Mounts: []container.MountPoint{},
	}
}

func TestContainerStopExpectationTransport(t *testing.T) {
	expected := stopExpectationFixture()
	var encoded bytes.Buffer
	require.NoError(t, json.NewEncoder(&encoded).Encode(expected))
	decoded, err := ParseContainerStopExpectation(encoded.Bytes())
	require.NoError(t, err)
	require.Equal(t, expected, decoded)
	for _, bad := range []string{
		strings.Replace(encoded.String(), `"mounts":[]`, `"mounts":null`, 1),
		strings.Replace(encoded.String(), `,"mounts":[]`, "", 1),
		strings.Replace(encoded.String(), `"labels":{`, `"extra":true,"labels":{`, 1),
		strings.Replace(encoded.String(), `"mounts":[]`, `"mounts":[],"mounts":[]`, 1),
		strings.Replace(encoded.String(), `"mounts":[]`, `"mounts":[],"\u006dounts":[]`, 1),
		encoded.String() + "{}",
	} {
		_, err := ParseContainerStopExpectation([]byte(bad))
		require.Error(t, err, bad)
	}
	for name, mutate := range map[string]func(*ContainerStopExpectation){
		"short ID":     func(e *ContainerStopExpectation) { e.ID = e.ID[:12] },
		"uppercase ID": func(e *ContainerStopExpectation) { e.ID = strings.ToUpper(e.ID) },
		"created":      func(e *ContainerStopExpectation) { e.Created = "" },
		"owner":        func(e *ContainerStopExpectation) { delete(e.Labels, LabelCodeflyOwner) },
		"name":         func(e *ContainerStopExpectation) { e.Name = "other" },
		"mounts":       func(e *ContainerStopExpectation) { e.Mounts = nil },
	} {
		t.Run(name, func(t *testing.T) {
			changed := expected
			changed.Labels = maps.Clone(expected.Labels)
			mutate(&changed)
			require.Error(t, changed.validate())
		})
	}

	// The producer reserves the actual default Encoder newline, and the same
	// validation is used by preview and direct execution.
	expected.Labels["padding"] = ""
	initial, err := json.Marshal(expected)
	require.NoError(t, err)
	expected.Labels["padding"] = strings.Repeat("x", MaxContainerStopExpectationBytes-len(initial)-1)
	require.NoError(t, expected.validate())
	encoded.Reset()
	require.NoError(t, json.NewEncoder(&encoded).Encode(expected))
	require.Equal(t, MaxContainerStopExpectationBytes, encoded.Len())
	_, err = ParseContainerStopExpectation(encoded.Bytes())
	require.NoError(t, err)
	_, err = ParseContainerStopExpectation(append(encoded.Bytes(), ' '))
	require.Error(t, err)
	expected.Labels["padding"] += "x"
	require.Error(t, expected.validate())
	compact, err := json.Marshal(expected)
	require.NoError(t, err)
	_, err = ParseContainerStopExpectation(compact)
	require.Error(t, err, "omitting the newline must not bypass the producer budget")
}

func TestContainerStopObservationAndMountEvidence(t *testing.T) {
	expected := stopExpectationFixture()
	inspect := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: expected.ID, Created: expected.Created, Name: "/" + expected.Name,
			HostConfig: &container.HostConfig{}, State: &container.State{}},
		Config: &container.Config{Labels: maps.Clone(expected.Labels)},
	}
	observed, err := containerStopObservation(inspect, expected.ID)
	require.NoError(t, err)
	require.Equal(t, expected, observed, "Docker nil mounts must produce explicit []")
	inspect.HostConfig.AutoRemove = true
	_, err = containerStopObservation(inspect, expected.ID)
	require.ErrorContains(t, err, "AutoRemove")
	inspect.HostConfig = nil
	_, err = containerStopObservation(inspect, expected.ID)
	require.Error(t, err)
	inspect.HostConfig = &container.HostConfig{}
	inspect.ID = strings.Repeat("b", 64)
	_, err = containerStopObservation(inspect, expected.ID)
	require.Error(t, err)

	a := container.MountPoint{Type: mount.TypeBind, Source: "/source", Destination: "/data", Mode: "rw", RW: true, Propagation: mount.PropagationRPrivate}
	b := container.MountPoint{Type: mount.TypeVolume, Name: "volume", Source: "/volume", Destination: "/other", Driver: "local", RW: true}
	require.Equal(t, canonicalContainerMounts([]container.MountPoint{a, b}), canonicalContainerMounts([]container.MountPoint{b, a}))
	for _, changed := range []container.MountPoint{
		{Type: a.Type, Source: a.Source, Destination: a.Destination, Mode: a.Mode, RW: false, Propagation: a.Propagation},
		{Type: a.Type, Source: "/replacement", Destination: a.Destination, Mode: a.Mode, RW: a.RW, Propagation: a.Propagation},
	} {
		require.NotEqual(t, canonicalContainerMounts([]container.MountPoint{a}), canonicalContainerMounts([]container.MountPoint{changed}))
	}
}

func TestContainerStopExpectationRefusesSchemaAliases(t *testing.T) {
	expected := stopExpectationFixture()
	expected.Labels["Tenant"], expected.Labels["tenant"] = "upper", "lower"
	expected.Mounts = []container.MountPoint{{Type: mount.TypeVolume, Name: "volume", Source: "/source", Destination: "/data", Driver: "local", Mode: "rw", RW: true, Propagation: mount.PropagationRPrivate}}
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	for _, field := range []string{"id", "created", "name", "labels", "mounts"} {
		t.Run(field, func(t *testing.T) {
			// A single noncanonical spelling and an overriding alias both
			// refuse; ordering must not choose which evidence is authoritative.
			alias := strings.ToUpper(field)
			changed := strings.Replace(string(data), `"`+field+`":`, `"`+alias+`":`, 1)
			_, err := ParseContainerStopExpectation([]byte(changed))
			require.Error(t, err)
		})
	}
	for _, field := range []string{"Type", "Name", "Source", "Destination", "Driver", "Mode", "RW", "Propagation"} {
		t.Run("mount_"+field, func(t *testing.T) {
			alias := strings.ToLower(field)
			changed := strings.Replace(string(data), `"`+field+`":`, `"`+alias+`":`, 1)
			_, err := ParseContainerStopExpectation([]byte(changed))
			require.Error(t, err)
		})
	}
	for _, injected := range []string{
		`"ID":"` + strings.Repeat("b", 64) + `",`,
		`"Name":"other",`,
		`"\u0049D":"` + strings.Repeat("b", 64) + `",`,
	} {
		_, err := ParseContainerStopExpectation([]byte("{" + injected + string(data[1:])))
		require.Error(t, err)
		_, err = ParseContainerStopExpectation([]byte(string(data[:len(data)-1]) + "," + strings.TrimSuffix(injected, ",") + "}"))
		require.Error(t, err)
	}
	for _, pair := range []string{`"RW":true,"rw":false`, `"rw":false,"RW":true`, `"RW":true,"\u0052W":false`} {
		_, err := ParseContainerStopExpectation([]byte(strings.Replace(string(data), `"RW":true`, pair, 1)))
		require.Error(t, err)
	}
	_, err = ParseContainerStopExpectation([]byte(strings.Replace(string(data), `"RW":true,`, "", 1)))
	require.Error(t, err, "a missing mount field is not its zero value")
	// Legal escaped spelling, whitespace and dynamic case-sensitive labels
	// retain their meaning; this is schema strictness, not canonical JSON.
	decoded, err := ParseContainerStopExpectation([]byte(" \n" + strings.Replace(string(data), `"id":`, `"\u0069d":`, 1) + "\n"))
	require.NoError(t, err)
	require.Equal(t, expected, decoded)
}
