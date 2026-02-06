package resources

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/wool"
)

const JobConfigurationName = "job.codefly.yaml"

// JobAgent is the agent kind for jobs
const JobAgent = AgentKind("codefly:job")

// SplitUnique splits a unique identifier (module/name) into module and name parts
func SplitUnique(unique string) (module, name string) {
	parts := strings.Split(unique, "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// JobExecutionType defines how a job is executed
type JobExecutionType string

const (
	JobExecutionOneShot   JobExecutionType = "one-shot"
	JobExecutionScheduled JobExecutionType = "scheduled"
	JobExecutionTriggered JobExecutionType = "triggered"
)

// JobExecution configures how a job runs
type JobExecution struct {
	Type       JobExecutionType `yaml:"type"`
	Schedule   string           `yaml:"schedule,omitempty"`    // cron expression for scheduled jobs
	Timeout    string           `yaml:"timeout,omitempty"`     // e.g., "5m", "1h"
	Retries    int              `yaml:"retries,omitempty"`     // retry count on failure
	RetryDelay string           `yaml:"retry-delay,omitempty"` // delay between retries
}

// GetTimeout returns the timeout as a duration
func (e *JobExecution) GetTimeout() time.Duration {
	if e == nil || e.Timeout == "" {
		return 30 * time.Minute // default timeout
	}
	d, err := time.ParseDuration(e.Timeout)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}

// GetRetryDelay returns the retry delay as a duration
func (e *JobExecution) GetRetryDelay() time.Duration {
	if e == nil || e.RetryDelay == "" {
		return 30 * time.Second // default retry delay
	}
	d, err := time.ParseDuration(e.RetryDelay)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// JobDependency represents a dependency on another job
type JobDependency struct {
	Name   string `yaml:"name"`
	Module string `yaml:"module,omitempty"`
}

// Job represents an ephemeral execution unit for scheduled or one-shot tasks
type Job struct {
	Kind        string `yaml:"kind"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Version     string `yaml:"version"`

	// Execution configuration
	Execution *JobExecution `yaml:"execution,omitempty"`

	// Agent for running the job
	Agent *Agent `yaml:"agent,omitempty"`

	// Dependencies
	ServiceDependencies                []*ServiceDependency `yaml:"service-dependencies,omitempty"`
	JobDependencies                    []*JobDependency     `yaml:"job-dependencies,omitempty"`
	LibraryDependencies                []*LibraryDependency `yaml:"library-dependencies,omitempty"`
	WorkspaceConfigurationDependencies []string             `yaml:"workspace-configuration-dependencies,omitempty"`

	// Job-specific settings
	Spec map[string]any `yaml:"spec,omitempty"`

	// Internal
	dir    string
	module string
}

// JobReference is used by modules to reference jobs
type JobReference struct {
	Name         string  `yaml:"name"`
	Module       string  `yaml:"-"`
	PathOverride *string `yaml:"path,omitempty"`
}

// JobIdentity uniquely identifies a job
type JobIdentity struct {
	Name      string
	Module    string
	Workspace string
	Version   string
}

// Unique returns a unique identifier for the job
func (j *JobIdentity) Unique() string {
	if j.Module == "" {
		return j.Name
	}
	return path.Join(j.Module, j.Name)
}

// NewJob creates a new Job
func NewJob(ctx context.Context, name string) (*Job, error) {
	w := wool.Get(ctx).In("NewJob", wool.NameField(name))

	// Validate name
	if name == "" {
		return nil, w.NewError("job name cannot be empty")
	}

	job := &Job{
		Kind:    "job",
		Name:    name,
		Version: "0.0.1",
		Execution: &JobExecution{
			Type:    JobExecutionOneShot,
			Timeout: "30m",
			Retries: 0,
		},
	}

	return job, nil
}

// Dir returns the job directory
func (job *Job) Dir() string {
	return job.dir
}

// WithDir sets the job directory
func (job *Job) WithDir(dir string) {
	job.dir = dir
}

// Module returns the job's module name
func (job *Job) Module() string {
	return job.module
}

// SetModule sets the job's module
func (job *Job) SetModule(module string) {
	job.module = module
}

// Save saves the job configuration
func (job *Job) Save(ctx context.Context) error {
	return job.SaveToDir(ctx, job.dir)
}

// SaveToDir saves the job to a specific directory
func (job *Job) SaveToDir(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("Job.SaveToDir", wool.NameField(job.Name))

	if dir == "" {
		return w.NewError("job directory is empty")
	}

	return SaveToDir[Job](ctx, job, dir)
}

// Unique returns a unique identifier for the job
func (job *Job) Unique() string {
	if job.module == "" {
		return job.Name
	}
	return path.Join(job.module, job.Name)
}

// Identity returns the job identity
func (job *Job) Identity() *JobIdentity {
	return &JobIdentity{
		Name:    job.Name,
		Module:  job.module,
		Version: job.Version,
	}
}

// IsScheduled returns true if the job is scheduled
func (job *Job) IsScheduled() bool {
	return job.Execution != nil && job.Execution.Type == JobExecutionScheduled
}

// IsOneShot returns true if the job is one-shot
func (job *Job) IsOneShot() bool {
	return job.Execution == nil || job.Execution.Type == JobExecutionOneShot
}

// IsTriggered returns true if the job is triggered
func (job *Job) IsTriggered() bool {
	return job.Execution != nil && job.Execution.Type == JobExecutionTriggered
}

// Proto converts to a map representation
func (job *Job) Proto(_ context.Context) map[string]any {
	proto := map[string]any{
		"name":        job.Name,
		"description": job.Description,
		"version":     job.Version,
		"module":      job.module,
	}

	if job.Execution != nil {
		proto["execution"] = map[string]any{
			"type":        string(job.Execution.Type),
			"schedule":    job.Execution.Schedule,
			"timeout":     job.Execution.Timeout,
			"retries":     job.Execution.Retries,
			"retry_delay": job.Execution.RetryDelay,
		}
	}

	if job.Agent != nil {
		proto["agent"] = map[string]any{
			"kind":      string(job.Agent.Kind),
			"name":      job.Agent.Name,
			"version":   job.Agent.Version,
			"publisher": job.Agent.Publisher,
		}
	}

	return proto
}

// LoadJobFromDir loads a job from a directory
func LoadJobFromDir(ctx context.Context, dir string) (*Job, error) {
	w := wool.Get(ctx).In("LoadJobFromDir", wool.DirField(dir))

	job, err := LoadFromDir[Job](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	job.dir = dir
	return job, nil
}

// Module job management

// LoadJobFromName loads a job by name from a module
func (mod *Module) LoadJobFromName(ctx context.Context, name string) (*Job, error) {
	w := wool.Get(ctx).In("Module.LoadJobFromName", wool.NameField(name))

	for _, ref := range mod.JobReferences {
		if ReferenceMatch(ref.Name, name) {
			return mod.LoadJobFromReference(ctx, ref)
		}
	}

	return nil, w.Wrap(shared.NewErrorResourceNotFound("job", name))
}

// LoadJobFromReference loads a job from a reference
func (mod *Module) LoadJobFromReference(ctx context.Context, ref *JobReference) (*Job, error) {
	w := wool.Get(ctx).In("Module.LoadJobFromReference", wool.NameField(ref.Name))

	var jobDir string
	if ref.PathOverride != nil {
		jobDir = *ref.PathOverride
		if !filepath.IsAbs(jobDir) {
			jobDir = filepath.Join(mod.Dir(), jobDir)
		}
	} else {
		jobDir = filepath.Join(mod.Dir(), "jobs", ref.Name)
	}

	job, err := LoadJobFromDir(ctx, jobDir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	job.module = mod.Name
	return job, nil
}

// LoadJobs loads all jobs from a module
func (mod *Module) LoadJobs(ctx context.Context) ([]*Job, error) {
	var jobs []*Job
	for _, ref := range mod.JobReferences {
		job, err := mod.LoadJobFromReference(ctx, ref)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// NewJob creates a new job in the module
func (mod *Module) NewJob(ctx context.Context, name string) (*Job, error) {
	w := wool.Get(ctx).In("Module.NewJob", wool.NameField(name))

	// Check if job already exists
	_, err := mod.LoadJobFromName(ctx, name)
	if err == nil {
		return nil, w.NewError("job %s already exists in module %s", name, mod.Name)
	}

	job, err := NewJob(ctx, name)
	if err != nil {
		return nil, w.Wrap(err)
	}

	// Set up job directory
	jobDir := filepath.Join(mod.Dir(), "jobs", name)
	if _, err := shared.CheckDirectoryOrCreate(ctx, jobDir); err != nil {
		return nil, w.Wrapf(err, "failed to create job directory")
	}

	job.dir = jobDir
	job.module = mod.Name

	return job, nil
}

// AddJobReference adds a job reference to the module
func (mod *Module) AddJobReference(ctx context.Context, ref *JobReference) error {
	w := wool.Get(ctx).In("Module.AddJobReference", wool.NameField(ref.Name))

	// Check for duplicates
	for _, existing := range mod.JobReferences {
		if existing.Name == ref.Name {
			return w.NewError("job %s already exists in module", ref.Name)
		}
	}

	ref.Module = mod.Name
	mod.JobReferences = append(mod.JobReferences, ref)

	return nil
}

// Workspace job management

// LoadJobFromUnique loads a job by its unique identifier (module/job)
func (workspace *Workspace) LoadJobFromUnique(ctx context.Context, unique string) (*Job, error) {
	w := wool.Get(ctx).In("Workspace.LoadJobFromUnique", wool.Field("unique", unique))

	moduleName, jobName := SplitUnique(unique)

	mod, err := workspace.LoadModuleFromName(ctx, moduleName)
	if err != nil {
		return nil, w.Wrap(err)
	}

	return mod.LoadJobFromName(ctx, jobName)
}

// LoadAllJobs loads all jobs from all modules in the workspace
func (workspace *Workspace) LoadAllJobs(ctx context.Context) ([]*Job, error) {
	w := wool.Get(ctx).In("Workspace.LoadAllJobs")

	modules, err := workspace.LoadModules(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	var allJobs []*Job
	for _, mod := range modules {
		jobs, err := mod.LoadJobs(ctx)
		if err != nil {
			w.Warn("failed to load jobs from module", wool.Field("module", mod.Name), wool.ErrField(err))
			continue
		}
		allJobs = append(allJobs, jobs...)
	}

	return allJobs, nil
}

// FindJobByName finds a job by name across all modules
func (workspace *Workspace) FindJobByName(ctx context.Context, name string) (*Job, error) {
	w := wool.Get(ctx).In("Workspace.FindJobByName", wool.NameField(name))

	// Check if name contains module
	if moduleName, jobName := SplitUnique(name); moduleName != "" {
		return workspace.LoadJobFromUnique(ctx, path.Join(moduleName, jobName))
	}

	// Search all modules
	modules, err := workspace.LoadModules(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	var found *Job
	for _, mod := range modules {
		job, err := mod.LoadJobFromName(ctx, name)
		if err != nil {
			continue
		}
		if found != nil {
			return nil, w.NewError("ambiguous job name %s: found in multiple modules", name)
		}
		found = job
	}

	if found == nil {
		return nil, w.Wrap(shared.NewErrorResourceNotFound("job", name))
	}

	return found, nil
}

// DiscoverJobs discovers jobs in a module directory
func DiscoverJobs(ctx context.Context, moduleDir string) ([]*JobReference, error) {
	w := wool.Get(ctx).In("DiscoverJobs", wool.DirField(moduleDir))

	jobsDir := filepath.Join(moduleDir, "jobs")
	exists, err := shared.DirectoryExists(ctx, jobsDir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !exists {
		return nil, nil
	}

	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil, w.Wrapf(err, "failed to read jobs directory")
	}

	var refs []*JobReference
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check for job.codefly.yaml
		jobConfig := filepath.Join(jobsDir, entry.Name(), JobConfigurationName)
		if _, err := os.Stat(jobConfig); os.IsNotExist(err) {
			continue
		}

		refs = append(refs, &JobReference{
			Name: entry.Name(),
		})
	}

	return refs, nil
}
