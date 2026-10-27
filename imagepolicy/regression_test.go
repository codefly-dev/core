package imagepolicy_test

import (
	"testing"

	"github.com/codefly-dev/core/imagepolicy"
)

func TestRegistryBoundaries(t *testing.T) {
	policy := imagepolicy.Policy{AllowedRegistries: []string{"registry.example/team"}}
	for _, base := range []string{"registry.example/team-evil/app", "registry.example/team.evil/app"} {
		t.Run(base, func(t *testing.T) {
			requireRules(t, imagepolicy.Check("FROM "+base, policy), imagepolicy.ErrRegistryNotAllowed)
		})
	}
	requireRules(t, imagepolicy.Check("FROM registry.example.evil/team/app", imagepolicy.Policy{
		AllowedRegistries: []string{"registry.example"},
	}), imagepolicy.ErrRegistryNotAllowed)
	requireRules(t, imagepolicy.Check("FROM registry.example/team/app", policy))
	requireRules(t, imagepolicy.Check("FROM alpine", imagepolicy.Policy{AllowedRegistries: []string{"docker.io/library"}}))
}

func TestStageScope(t *testing.T) {
	for _, body := range []string{
		"FROM alpine AS alpine",
		"ARG BASE=alpine\nFROM ${BASE} AS alpine",
	} {
		requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true}), imagepolicy.ErrUnpinnedBase)
	}
	requireRules(t, imagepolicy.Check("FROM scratch AS build\nARG BASE=scratch\nFROM ${BASE}", imagepolicy.Policy{RequireDigest: true}), imagepolicy.ErrUnresolvedBase)
}

func TestFinalImageLabels(t *testing.T) {
	policy := imagepolicy.Policy{RequiredLabels: []string{"APPID"}}
	for _, body := range []string{
		"FROM scratch AS build\nLABEL APPID=build-only\nFROM scratch",
		"FROM scratch\nLABEL APPID=present\nLABEL APPID=\"\"",
		"FROM scratch\nLABEL APPID=$UNDEFINED",
		"FROM scratch\nARG ID=app\nENV ID=$UNDEFINED\nLABEL APPID=prefix-$ID",
		"FROM scratch\nRUN <<EOF\nLABEL APPID=not-an-instruction\nEOF",
	} {
		t.Run(body, func(t *testing.T) {
			requireRules(t, imagepolicy.Check(body, policy), imagepolicy.ErrMissingLabel)
		})
	}
	for _, body := range []string{
		"FROM scratch AS base\nLABEL APPID=app\nFROM base",
		"FROM scratch\nARG ID=app\nLABEL APPID=$ID",
		"FROM scratch\nENV ID=app\nLABEL APPID=$ID",
		"FROM scratch\nLABEL APPID app",
		"FROM scratch\nLABEL description=\"escaped \\\"quote\\\"\" APPID=app",
	} {
		t.Run(body, func(t *testing.T) {
			requireRules(t, imagepolicy.Check(body, policy))
		})
	}
}

func TestMalformedDigestIsRefused(t *testing.T) {
	requireRules(t, imagepolicy.Check("FROM alpine@sha256:pretend", imagepolicy.Policy{RequireDigest: true}), imagepolicy.ErrUnresolvedBase)
}

func TestZeroPolicyDoesNotResolveArguments(t *testing.T) {
	requireRules(t, imagepolicy.Check("ARG BASE\nFROM ${BASE}", imagepolicy.Policy{}))
}

func TestMalformedDockerfileIsRefused(t *testing.T) {
	for _, body := range []string{"", "FROM", "FROM scratch\nNOT_AN_INSTRUCTION value"} {
		requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true}), imagepolicy.ErrInvalidDockerfile)
	}
}
