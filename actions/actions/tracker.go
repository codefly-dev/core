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

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"
)

type ActionSave struct {
	Command string          `json:"command"`
	Data    json.RawMessage `json:"data"`
}

type ActionTracker struct {
	Dir    string
	Replay bool
}

func NewActionTracker(ctx context.Context, location string, group string) (*ActionTracker, error) {
	w := wool.Get(ctx).In("actions.NewActionTracker", wool.Field("group", group))
	_, err := shared.CheckDirectoryOrCreate(ctx, location)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create directory")
	}
	trac := &ActionTracker{
		Dir: location,
	}
	return trac, nil
}

func (tracker *ActionTracker) WithDir(dir string) {
	if dir == "" {
		return
	}
	tracker.Dir = dir
}

func (tracker *ActionTracker) NextStep() int {
	// Walk all files in the directory and extract the step number
	var num int
	_ = filepath.Walk(tracker.Dir, func(_ string, info os.FileInfo, err error) error {
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
	if tracker.Replay {
		return nil
	}
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
	filename := fmt.Sprintf("%d_%s.codefly.action.json", step, strings.ReplaceAll(actionName, "*", ""))
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
	err := filepath.Walk(tracker.Dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
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
	var as []Action
	for _, step := range steps {
		as = append(as, step.Action)
	}
	return as, nil
}

func SetActionTracker(actionTracker *ActionTracker) {
	tracker = actionTracker
}

var tracker *ActionTracker
