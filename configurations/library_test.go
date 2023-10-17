package configurations_test

//
//import (
//	"github.com/codefly-dev/core/configurations"
//	"github.com/stretchr/testify/assert"
//	"reflect"
//	"testing"
//)
//
//func TestParseLibrary(t *testing.T) {
//	s := "codefly.io/go-grpc"
//	lib, err := configurations.ParseLibrary(s)
//	assert.NoError(t, err)
//	assert.Equal(t, "go", lib.Kind)
//}
//
//func TestLibraryManager(t *testing.T) {
//
//	var libs []*configurations.LibrarySummary
//
//	manager := configurations.NewLibraryManager(libs)
//	back := manager.ToSummary()
//	assert.True(t, reflect.DeepEqual(libs, back), "nothing")
//
//	r, err := configurations.ParseLibrary("codefly.io/redis:0.0.0")
//	assert.NoError(t, err)
//	err = manager.Add(r, "path/old")
//	assert.NoError(t, err)
//
//	back = manager.ToSummary()
//	assert.Equal(t, 1, len(back))
//	assert.Equal(t, "go", back[0].Kind)
//	assert.Equal(t, 1, len(back), "only go libraries")
//	assert.Equal(t, 1, len(back[0].Bases), "only one base")
//	assert.Equal(t, 1, len(back[0].Bases[0].Usages), "one usage of the library")
//
//	// Map the same doesn't change anything
//	err = manager.Add(r, "path/old")
//	assert.NoError(t, err)
//
//	back = manager.ToSummary()
//	assert.Equal(t, 1, len(back))
//	assert.Equal(t, "go", back[0].Kind)
//	assert.Equal(t, 1, len(back), "only go libraries")
//	assert.Equal(t, 1, len(back[0].Bases), "only one base")
//	assert.Equal(t, 1, len(back[0].Bases[0].Usages), "one usage of the library")
//
//	// Map a new path
//	r, err = configurations.ParseLibrary("codefly.io/redis:0.0.0")
//	assert.NoError(t, err)
//	err = manager.Add(r, "path/new")
//	assert.NoError(t, err)
//
//	back = manager.ToSummary()
//	assert.Equal(t, 1, len(back))
//	assert.Equal(t, "go", back[0].Kind)
//	assert.Equal(t, 1, len(back), "still only go libraries")
//	assert.Equal(t, 1, len(back[0].Bases), "still only one library")
//	assert.Equal(t, 2, len(back[0].Bases[0].Usages), "two usages of the library")
//	assert.Equal(t, "path/old", back[0].Bases[0].Usages[0].RelativePath)
//	assert.Equal(t, "path/new", back[0].Bases[0].Usages[1].RelativePath)
//
//	// Map a new go library
//	// Map a new path
//	r, err = configurations.ParseLibrary("codefly.io/grpc:0.0.0")
//	assert.NoError(t, err)
//	err = manager.Add(r, "/path/new")
//	assert.NoError(t, err)
//
//	back = manager.ToSummary()
//	assert.Equal(t, 1, len(back))
//	assert.Equal(t, "go", back[0].Kind)
//	assert.Equal(t, 2, len(back[0].Bases), "two go libraries")
//	assert.Equal(t, "codefly.io/redis", back[0].Bases[0].Name)
//	assert.Equal(t, 2, len(back[0].Bases[0].Usages))
//	assert.Equal(t, "codefly.io/grpc", back[0].Bases[1].Name)
//	assert.Equal(t, 1, len(back[0].Bases[1].Usages))
//}
