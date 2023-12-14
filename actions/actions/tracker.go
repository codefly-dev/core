package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

type ActionSave struct {
	Command string          `json:"command"`
	Data    json.RawMessage `json:"data"`
}

type ActionTracker struct {
	Dir string
}

func NewActionTracker(group string) *ActionTracker {
	ctx := shared.NewContext()
	dir := path.Join(configurations.WorkspaceConfigurationDir(), "actions", group)
	err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		shared.GetLogger(ctx).Warn("cannot create action directory: %v", err)
		return nil
	}
	tracker = &ActionTracker{
		Dir: dir,
	}
	return tracker
}

func InitActionTracker(group string) {
	tracker = NewActionTracker(group)
}

func (tracker *ActionTracker) NextStep() int {
	// Walk all files in the directory and extract the step number
	var num int
	_ = filepath.Walk(tracker.Dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		filename := info.Name()
		if !strings.HasSuffix(filename, ".json") {
			return nil
		}
		// Extract the step number
		s := strings.Split(filename, "_")[0]
		// Convert to int
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		if n > num {
			num = n
		}
		return nil
	})
	return num + 1
}

func (tracker *ActionTracker) Save(action Action) error {
	cmd := action.Command()
	data, err := json.Marshal(action)
	if err != nil {
		return err
	}
	actionSave := &ActionSave{
		Command: cmd,
		Data:    data,
	}
	data, err = json.Marshal(actionSave)
	if err != nil {
		return err
	}
	step := tracker.NextStep()
	actionName := strings.ToLower(fmt.Sprintf("%T", action))
	filename := fmt.Sprintf("%d_%s.codefly.action.json", step, strings.Replace(actionName, "*", "", -1))
	err = os.WriteFile(path.Join(tracker.Dir, filename), data, 0600)
	if err != nil {
		return err
	}
	return nil
}

type ActionStep struct {
	Step   int
	Data   *json.RawMessage `json:"data"`
	Action Action
}

func (tracker *ActionTracker) GetActions(_ context.Context) ([]Action, error) {
	var steps []ActionStep
	err := filepath.Walk(tracker.Dir, func(path string, info os.FileInfo, e error) error {
		if info.IsDir() {
			return nil
		}
		filename := info.Name()
		if !strings.HasSuffix(filename, ".codefly.action.json") {
			return nil
		}
		// Extract the step number
		s := strings.Split(filename, "_")[0]
		// Convert to int
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		actionStep := ActionStep{Step: n}
		err = json.Unmarshal(data, &actionStep)
		if err != nil {
			return err
		}
		content, err := actionStep.Data.MarshalJSON()
		if err != nil {
			return err
		}
		action, err := CreateAction(content)
		if err != nil {
			return err
		}
		actionStep.Action = action
		steps = append(steps, actionStep)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Sort by step
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].Step < steps[j].Step
	})
	var actions []Action
	for _, step := range steps {
		actions = append(actions, step.Action)
	}
	return actions, nil
}
