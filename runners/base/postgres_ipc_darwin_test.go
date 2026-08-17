//go:build darwin

package base

import (
	"reflect"
	"testing"
)

func TestPostgresIPCRemovalPlanPreservesLiveControlSegmentAndRemovesOrphans(t *testing.T) {
	shared := `
T ID KEY MODE OWNER GROUP CREATOR CGROUP NATTCH SEGSZ CPID LPID
m 90 0x00001000 --rw------- alice staff alice staff 4 56 200 200
m 91 0x00002000 --rw------- alice staff alice staff 0 56 300 300
m 92 0x00003000 --rw------- alice staff alice staff 0 4096 400 400
m 93 0x00004000 --rw------- bob staff bob staff 0 56 500 500
`
	semaphores := `
T ID KEY MODE OWNER GROUP CREATOR CGROUP NSEMS OTIME CTIME
s 10 0x00001001 --ra------- alice staff alice staff 20 10:00:00 10:00:00
s 11 0x00001002 --ra------- alice staff alice staff 20 10:00:00 10:00:00
s 20 0x00002001 --ra------- alice staff alice staff 20 09:00:00 09:00:00
s 21 0x00002002 --ra------- alice staff alice staff 20 09:00:00 09:00:00
s 30 0x00005001 --ra------- alice staff alice staff 20 08:00:00 08:00:00
s 40 0x00006001 --ra------- alice staff alice staff 20 07:00:00 07:00:00
s 41 0x00006002 --ra------- alice staff alice staff 20 07:00:00 07:00:00
s 42 0x00007001 --ra------- alice staff alice staff 20 06:30:00 06:30:00
s 43 0x00007005 --ra------- alice staff alice staff 20 06:30:00 06:30:00
s 44 0x00001003 --ra------- alice staff alice staff 20 10:00:00 10:00:00
s 45 0x00001004 --ra------- alice staff alice staff 20 10:00:00 10:00:00
s 46 0x00009001 --ra------- alice staff alice staff 20 06:00:00 06:00:00
s 47 0x00009002 --ra------- alice staff alice staff 20 06:00:01 06:00:01
s 50 0x00007001 --rw------- alice staff alice staff 20 06:00:00 06:00:00
s 60 0x00008001 --ra------- bob staff bob staff 20 05:00:00 05:00:00
s 61 0x00008002 --ra------- bob staff bob staff 20 05:00:00 05:00:00
`
	sharedIDs, semaphoreIDs, err := postgresIPCRemovalPlan(shared, semaphores, "alice", func(pid int) bool {
		return pid == 200
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sharedIDs, []int{91}) {
		t.Fatalf("shared-memory removals = %v, want [91]", sharedIDs)
	}
	if !reflect.DeepEqual(semaphoreIDs, []int{20, 21, 40, 41, 42, 43}) {
		t.Fatalf("semaphore removals = %v, want [20 21 40 41 42 43]", semaphoreIDs)
	}
}

func TestPostgresIPCRemovalPlanRejectsMalformedCandidateRows(t *testing.T) {
	if _, _, err := postgresIPCRemovalPlan(
		"m bad 0x1000 --rw------- alice staff alice staff 0 56 300 300",
		"",
		"alice",
		func(int) bool { return false },
	); err == nil {
		t.Fatal("malformed PostgreSQL control segment was accepted")
	}
	if _, _, err := postgresIPCRemovalPlan(
		"",
		"s bad 0x1001 --ra------- alice staff alice staff 20 10:00:00 10:00:00",
		"alice",
		func(int) bool { return false },
	); err == nil {
		t.Fatal("malformed PostgreSQL semaphore set was accepted")
	}
}
