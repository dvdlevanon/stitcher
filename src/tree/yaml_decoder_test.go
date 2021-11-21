package tree

import (
	"os"
	"testing"

	"github.com/dvdlevanon/stitcher/src/mocks"
	"github.com/dvdlevanon/stitcher/src/registry"
	"github.com/go-errors/errors"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

//go:generate /home/david/temp/temp/2021-11-16-223313/mock_1.6.0_linux_amd64/mockgen -destination=../mocks/structs_mocks.go -source ../model/structs.go -package mocks
//go:generate /home/david/temp/temp/2021-11-16-223313/mock_1.6.0_linux_amd64/mockgen -destination=../mocks/registry_mocks.go -source ../registry/registry.go -package mocks

func TestDirectoryName(t *testing.T) {
	dir := newDirectory("test", nil)
	require.Equal(t, dir.GetName(), "test")
}

func TestParent(t *testing.T) {
	parent := newDirectory("parent", nil)
	dir := newDirectory("test", parent)
	require.Equal(t, dir.GetParent(), parent)
}

func TestDirectoryChildren(t *testing.T) {
	parent := newDirectory("parent", nil)
	child1 := newDirectory("child1", parent)
	child2 := newDirectory("child2", parent)
	parent.children[child1.GetName()] = child1
	parent.children[child2.GetName()] = child2
	require.Len(t, parent.GetChildren(), 2)
	require.NotNil(t, parent.GetChildren()[0])
	require.NotNil(t, parent.GetChildren()[1])
	children := parent.GetChildren()
	require.NotEqual(t, children[0].GetName(), children[1].GetName())
}

func TestDirectoryTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	parent := newDirectory("parent", nil)
	task1 := mocks.NewMockTask(ctrl)
	task2 := mocks.NewMockTask(ctrl)
	task1.EXPECT().GetName().Return("task1").AnyTimes()
	task2.EXPECT().GetName().Return("task2").AnyTimes()
	parent.tasks[task1.GetName()] = task1
	parent.tasks[task2.GetName()] = task2
	require.Len(t, parent.GetTasks(), 2)
	require.NotNil(t, parent.GetTasks()[0])
	require.NotNil(t, parent.GetTasks()[1])
	tasks := parent.GetTasks()
	require.NotEqual(t, tasks[0].GetName(), tasks[1].GetName())
}

func TestEmptyYaml(t *testing.T) {
	root, err := DecodeBytes([]byte(""))
	require.NoError(t, err)
	require.Nil(t, root.GetParent())
	require.Equal(t, root.GetName(), "")
	require.Empty(t, root.GetChildren())
	require.Empty(t, root.GetTasks())
}

func TestInvalidYaml(t *testing.T) {
	_, err := DecodeBytes([]byte("asd	fds"))
	require.Error(t, err)
}

func TestYamlFile(t *testing.T) {
	yamlfile, err := os.CreateTemp("", "")
	require.NoError(t, err)
	yamlfile.WriteString("./from-file:")
	defer yamlfile.Close()

	root, err := DecodeFile(yamlfile.Name())
	require.NoError(t, err)
	require.Nil(t, root.GetParent())
	require.Equal(t, root.GetName(), "")
	require.Empty(t, root.GetTasks())
	require.Len(t, root.GetChildren(), 1)
	require.Equal(t, root.GetChildren()[0].GetName(), "from-file")
	require.Equal(t, root.GetChildren()[0].GetParent(), root)
	require.Empty(t, root.GetChildren()[0].GetChildren())
	require.Empty(t, root.GetChildren()[0].GetTasks())
}

func TestMissingYamlFile(t *testing.T) {
	_, err := DecodeFile("/some/not/exists.yaml")
	require.Error(t, err)
}

func TestSingleDirecotory(t *testing.T) {
	root, err := DecodeBytes([]byte("./dir:"))
	require.NoError(t, err)
	require.Nil(t, root.GetParent())
	require.Equal(t, root.GetName(), "")
	require.Empty(t, root.GetTasks())
	require.Len(t, root.GetChildren(), 1)
	require.Equal(t, root.GetChildren()[0].GetName(), "dir")
	require.Equal(t, root.GetChildren()[0].GetParent(), root)
	require.Empty(t, root.GetChildren()[0].GetChildren())
	require.Empty(t, root.GetChildren()[0].GetTasks())
}

func TestNestedDirectories(t *testing.T) {
	root, err := DecodeBytes([]byte(`
./parent:
  ./nested1:
`))
	require.NoError(t, err)
	require.Nil(t, root.GetParent())
	require.Equal(t, root.GetName(), "")
	require.Empty(t, root.GetTasks())
	require.Len(t, root.GetChildren(), 1)
	dir := root.GetChildren()[0]
	require.Equal(t, dir.GetName(), "parent")
	require.Equal(t, dir.GetParent(), root)
	require.Empty(t, dir.GetTasks())
	require.Len(t, dir.GetChildren(), 1)
	require.Equal(t, dir.GetChildren()[0].GetName(), "nested1")
	require.Equal(t, dir.GetChildren()[0].GetParent(), dir)
	require.Empty(t, dir.GetChildren()[0].GetTasks())
	require.Empty(t, dir.GetChildren()[0].GetChildren())
}
func TestSingleTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)
	err := registry.Register("type", builder)
	require.NoError(t, err)

	y := make(map[interface{}]interface{})
	yaml.Unmarshal([]byte("type.name:"), y)
	root := newDirectory("", nil)

	task := mocks.NewMockTask(ctrl)
	task.EXPECT().GetName().Return("name").AnyTimes()
	task.EXPECT().GetType().Return("type").AnyTimes()
	task.EXPECT().GetParent().Return(root).AnyTimes()
	task.EXPECT().GetId().Return("type.name").AnyTimes()
	builder.EXPECT().Build("name", root).Return(task, nil)

	err = root.decodeYaml(y)
	require.NoError(t, err)
	require.Nil(t, root.GetParent())
	require.Equal(t, root.GetName(), "")
	require.Empty(t, root.GetChildren())
	require.Len(t, root.GetTasks(), 1)
	require.Equal(t, root.GetTasks()[0].GetName(), "name")
	require.Equal(t, root.GetTasks()[0].GetId(), "type.name")
	require.Equal(t, root.GetTasks()[0].GetType(), "type")
	require.Equal(t, root.GetTasks()[0].GetParent(), root)
}

func TestInvalidTaskParent(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)

	err := registry.Register("invalid-type", builder)
	require.NoError(t, err)

	y := make(map[interface{}]interface{})
	yaml.Unmarshal([]byte("invalid-type.name:"), y)
	root := newDirectory("", nil)

	task := mocks.NewMockTask(ctrl)
	task.EXPECT().GetName().Return("name").AnyTimes()
	task.EXPECT().GetType().Return("invalid-type").AnyTimes()
	task.EXPECT().GetParent().Return(nil).AnyTimes()
	task.EXPECT().GetId().Return("invalid-type.name").AnyTimes()
	builder.EXPECT().Build("name", root).Return(task, nil)

	err = root.decodeYaml(y)
	require.Error(t, err)
}

func TestInvalidTaskName(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)

	err := registry.Register("invalid-name", builder)
	require.NoError(t, err)

	y := make(map[interface{}]interface{})
	yaml.Unmarshal([]byte("invalid-name.name:"), y)
	root := newDirectory("", nil)

	task := mocks.NewMockTask(ctrl)
	task.EXPECT().GetName().Return("bug").AnyTimes()
	task.EXPECT().GetType().Return("invalid-name").AnyTimes()
	task.EXPECT().GetParent().Return(root).AnyTimes()
	task.EXPECT().GetId().Return("invalid-name.name").AnyTimes()
	builder.EXPECT().Build("name", root).Return(task, nil)

	err = root.decodeYaml(y)
	require.Error(t, err)
}

func TestErrorTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)

	err := registry.Register("error-task", builder)
	require.NoError(t, err)

	y := make(map[interface{}]interface{})
	yaml.Unmarshal([]byte("error-task.name:"), y)
	root := newDirectory("", nil)

	builder.EXPECT().Build("name", root).Return(nil, errors.Errorf("test error"))

	err = root.decodeYaml(y)
	require.Error(t, err)
}

func TestNilTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)

	err := registry.Register("nil-task", builder)
	require.NoError(t, err)

	y := make(map[interface{}]interface{})
	yaml.Unmarshal([]byte("nil-task.name:"), y)
	root := newDirectory("", nil)

	builder.EXPECT().Build("name", root).Return(nil, nil)

	err = root.decodeYaml(y)
	require.Error(t, err)
}

func TestInvalidTaskId(t *testing.T) {
	_, err := DecodeBytes([]byte(`
./dir:
  invalid.task.id:
`))
	require.Error(t, err)
}
