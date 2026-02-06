package resources_test

import (
	"context"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestLoadJob(t *testing.T) {
	ctx := context.Background()

	job, err := resources.LoadJobFromDir(ctx, "testdata/workspaces/with-jobs/jobs/db-migration")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "db-migration", job.Name)
	require.Equal(t, "Run database migrations", job.Description)
	require.Equal(t, "0.0.1", job.Version)

	// Check execution config
	require.NotNil(t, job.Execution)
	require.Equal(t, resources.JobExecutionOneShot, job.Execution.Type)
	require.Equal(t, "5m", job.Execution.Timeout)
	require.Equal(t, 3, job.Execution.Retries)
	require.Equal(t, "30s", job.Execution.RetryDelay)

	// Check agent
	require.NotNil(t, job.Agent)
	require.Equal(t, "go-job", job.Agent.Name)

	// Check service dependencies
	require.Len(t, job.ServiceDependencies, 1)
	require.Equal(t, "postgres", job.ServiceDependencies[0].Name)

	// Check spec
	require.NotNil(t, job.Spec)
	require.Equal(t, "./migrations", job.Spec["migration-dir"])
}

func TestModuleLoadJob(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-jobs")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	module, err := workspace.LoadModuleFromName(ctx, "with-jobs")
	require.NoError(t, err)
	require.NotNil(t, module)

	job, err := module.LoadJobFromName(ctx, "db-migration")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "db-migration", job.Name)
	require.Equal(t, "with-jobs", job.Module())
}

func TestModuleLoadJobs(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-jobs")
	require.NoError(t, err)

	module, err := workspace.LoadModuleFromName(ctx, "with-jobs")
	require.NoError(t, err)

	jobs, err := module.LoadJobs(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "db-migration", jobs[0].Name)
}

func TestWorkspaceLoadAllJobs(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-jobs")
	require.NoError(t, err)

	jobs, err := workspace.LoadAllJobs(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "db-migration", jobs[0].Name)
}

func TestWorkspaceFindJobByName(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-jobs")
	require.NoError(t, err)

	// Find by name only
	job, err := workspace.FindJobByName(ctx, "db-migration")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "db-migration", job.Name)

	// Find by unique (module/job)
	job, err = workspace.FindJobByName(ctx, "with-jobs/db-migration")
	require.NoError(t, err)
	require.NotNil(t, job)

	// Not found
	_, err = workspace.FindJobByName(ctx, "nonexistent")
	require.Error(t, err)
}

func TestJobIdentity(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-jobs")
	require.NoError(t, err)

	module, err := workspace.LoadModuleFromName(ctx, "with-jobs")
	require.NoError(t, err)

	job, err := module.LoadJobFromName(ctx, "db-migration")
	require.NoError(t, err)

	identity := job.Identity()
	require.NotNil(t, identity)
	require.Equal(t, "db-migration", identity.Name)
	require.Equal(t, "with-jobs", identity.Module)
	require.Equal(t, "0.0.1", identity.Version)
	require.Equal(t, "with-jobs/db-migration", identity.Unique())
}

func TestJobExecution(t *testing.T) {
	ctx := context.Background()

	job, err := resources.LoadJobFromDir(ctx, "testdata/workspaces/with-jobs/jobs/db-migration")
	require.NoError(t, err)

	// Test GetTimeout
	timeout := job.Execution.GetTimeout()
	require.Equal(t, 5*time.Minute, timeout)

	// Test GetRetryDelay
	delay := job.Execution.GetRetryDelay()
	require.Equal(t, 30*time.Second, delay)

	// Test execution type helpers
	require.True(t, job.IsOneShot())
	require.False(t, job.IsScheduled())
	require.False(t, job.IsTriggered())
}

func TestJobExecutionDefaults(t *testing.T) {
	// Test nil execution
	var exec *resources.JobExecution
	require.Equal(t, 30*time.Minute, exec.GetTimeout())
	require.Equal(t, 30*time.Second, exec.GetRetryDelay())

	// Test empty values
	exec = &resources.JobExecution{}
	require.Equal(t, 30*time.Minute, exec.GetTimeout())
	require.Equal(t, 30*time.Second, exec.GetRetryDelay())
}

func TestNewJob(t *testing.T) {
	ctx := context.Background()

	job, err := resources.NewJob(ctx, "test-job")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "test-job", job.Name)
	require.Equal(t, "job", job.Kind)
	require.Equal(t, "0.0.1", job.Version)
	require.NotNil(t, job.Execution)
	require.Equal(t, resources.JobExecutionOneShot, job.Execution.Type)

	// Empty name should fail
	_, err = resources.NewJob(ctx, "")
	require.Error(t, err)
}

func TestSplitUnique(t *testing.T) {
	module, name := resources.SplitUnique("backend/api")
	require.Equal(t, "backend", module)
	require.Equal(t, "api", name)

	module, name = resources.SplitUnique("simple")
	require.Equal(t, "", module)
	require.Equal(t, "simple", name)
}
